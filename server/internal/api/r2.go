package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type r2Config struct {
	AccountID       string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Endpoint        string
}

func (c r2Config) Ready() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != "" && c.Endpoint != ""
}

func (h *handlers) effectiveR2(ctx context.Context) r2Config {
	cfg := r2Config{
		Bucket:   h.setting(ctx, settingR2Bucket, strings.TrimSpace(h.cfg.R2Bucket)),
		Endpoint: strings.TrimRight(h.setting(ctx, settingR2Endpoint, strings.TrimSpace(h.cfg.R2Endpoint)), "/"),
	}
	cfg.AccountID = firstNonEmpty(
		h.setting(ctx, settingCFAccount, strings.TrimSpace(h.cfg.CloudflareAccountID)),
		h.setting(ctx, settingR2Account, strings.TrimSpace(h.cfg.R2AccountID)),
	)

	// 统一凭证：R2 与 Email Sending 共用 cloudflare_api_token。
	// Cloudflare API Token 的 S3 凭证规则：Access Key ID = Token ID，Secret = SHA256(Token)。
	token := strings.TrimSpace(h.setting(ctx, settingCFToken, h.cfg.CloudflareAPIToken))
	if token != "" {
		if id, err := cloudflareTokenID(ctx, token); err == nil && id != "" {
			cfg.AccessKeyID = id
			cfg.SecretAccessKey = sha256Hex(token)
		}
	}
	// 兼容旧的独立 R2 Access Key（未配置统一 Token 时）。
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = h.setting(ctx, settingR2AccessKey, strings.TrimSpace(h.cfg.R2AccessKeyID))
		cfg.SecretAccessKey = h.setting(ctx, settingR2SecretKey, strings.TrimSpace(h.cfg.R2SecretAccessKey))
	}
	if cfg.Endpoint == "" && cfg.AccountID != "" {
		cfg.Endpoint = "https://" + cfg.AccountID + ".r2.cloudflarestorage.com"
	}
	return cfg
}

type cachedCloudflareToken struct {
	id  string
	exp time.Time
}

var cloudflareTokenIDs sync.Map

// cloudflareTokenID 用 /user/tokens/verify 从 Token 本身取得 Token ID（R2 的 Access Key ID）。
func cloudflareTokenID(ctx context.Context, token string) (string, error) {
	cacheKey := sha256Hex(token)
	if v, ok := cloudflareTokenIDs.Load(cacheKey); ok {
		if c, ok := v.(cachedCloudflareToken); ok && time.Now().Before(c.exp) {
			return c.id, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Cloudflare Token 校验失败（HTTP %d）", resp.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
		Result  struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !out.Success || out.Result.ID == "" {
		return "", errors.New("Cloudflare Token 无效或缺少 ID")
	}
	cloudflareTokenIDs.Store(cacheKey, cachedCloudflareToken{id: out.Result.ID, exp: time.Now().Add(30 * time.Minute)})
	return out.Result.ID, nil
}

type r2Client struct {
	cfg r2Config
	hc  *http.Client
}

func newR2Client(cfg r2Config) *r2Client {
	return &r2Client{cfg: cfg, hc: &http.Client{Timeout: 60 * time.Second}}
}

func awsEncode(s string, keepSlash bool) string {
	e := url.PathEscape(s)
	if keepSlash {
		e = strings.ReplaceAll(e, "%2F", "/")
	}
	return e
}

func (c *r2Client) sign(req *http.Request, payloadHash string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	// canonical query 必须与 URL 中顺序一致（按键/值排序）
	query := req.URL.Query()
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vals := query[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEncode(k, false)+"="+awsEncode(v, false))
		}
	}
	canonicalQuery := strings.Join(parts, "&")
	req.URL.RawQuery = canonicalQuery

	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := req.Method + "\n" + canonicalURI + "\n" + canonicalQuery + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash
	scope := date + "/auto/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" +
		sha256Hex(canonicalRequest)

	kDate := hmacSHA256([]byte("AWS4"+c.cfg.SecretAccessKey), date)
	kRegion := hmacSHA256(kDate, "auto")
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.cfg.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func (c *r2Client) newRequest(ctx context.Context, method, key string, query url.Values, body io.Reader, size int64) (*http.Request, error) {
	if !c.cfg.Ready() {
		return nil, errors.New("R2 未配置")
	}
	u, err := url.Parse(c.cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("R2 endpoint 无效: %w", err)
	}
	u.Path = "/" + c.cfg.Bucket
	if key != "" {
		u.Path += "/" + key
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	return req, nil
}

type r2Object struct {
	Key          string `xml:"Key"`
	Size         int64  `xml:"Size"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
}

type r2ListResult struct {
	Contents []r2Object `xml:"Contents"`
	Folders  []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (c *r2Client) list(ctx context.Context, prefix, token string, maxKeys int) (r2ListResult, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("delimiter", "/")
	q.Set("max-keys", fmt.Sprint(maxKeys))
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if token != "" {
		q.Set("continuation-token", token)
	}
	req, err := c.newRequest(ctx, http.MethodGet, "", q, nil, -1)
	if err != nil {
		return r2ListResult{}, err
	}
	c.sign(req, sha256Hex(""))
	resp, err := c.hc.Do(req)
	if err != nil {
		return r2ListResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return r2ListResult{}, fmt.Errorf("R2 列表失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out r2ListResult
	if err := xml.NewDecoder(resp.Body).Decode(&out); err != nil {
		return r2ListResult{}, err
	}
	return out, nil
}

func (c *r2Client) put(ctx context.Context, key string, body io.Reader, size int64, payloadHash, contentType string) error {
	req, err := c.newRequest(ctx, http.MethodPut, key, nil, body, size)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.sign(req, payloadHash)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("R2 上传失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c *r2Client) get(ctx context.Context, key string) (*http.Response, error) {
	req, err := c.newRequest(ctx, http.MethodGet, key, nil, nil, -1)
	if err != nil {
		return nil, err
	}
	c.sign(req, sha256Hex(""))
	return c.hc.Do(req)
}

func (c *r2Client) delete(ctx context.Context, key string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, key, nil, nil, -1)
	if err != nil {
		return err
	}
	c.sign(req, sha256Hex(""))
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("R2 删除失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
