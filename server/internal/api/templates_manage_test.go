package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestNormalizeTemplateBucketKey(t *testing.T) {
	ok := map[string]string{
		"rule/configs/global_config.yaml": "rule/configs/global_config.yaml",
		"/rule/configs/a.yml":             "rule/configs/a.yml",
		"rule/configs/a.YAML":             "rule/configs/a.YAML",
	}
	for in, want := range ok {
		got, err := normalizeTemplateBucketKey(in)
		if err != nil || got != want {
			t.Fatalf("normalize(%q)=%q,%v，期望 %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "rule/configs/a.json", "rule/configs/a.yaml.bak", "other/a.yaml",
		"rule/configs/../a.yaml", "rule/configs/%2e%2e/a.yaml",
		"https://s3.proxypilot.yangzifun.org/rule/configs/a.yaml",
		"rule/configs/a.yaml?x=1", "rule/configs/a b.yaml",
	}
	for _, in := range bad {
		if got, err := normalizeTemplateBucketKey(in); err == nil {
			t.Fatalf("非法 key %q 应拒绝，got %q", in, got)
		}
	}
}

func TestTemplateBucketURLDerivation(t *testing.T) {
	h := &handlers{cfg: config.Config{}}
	base := h.templateBucketBase(context.Background())
	if base != defaultTemplateBucketBase {
		t.Fatalf("默认模板基址 = %q，期望 %q", base, defaultTemplateBucketBase)
	}
	key := "rule/configs/global_config.yaml"
	raw := templateBucketURL(base, key)
	want := "https://s3.proxypilot.yangzifun.org/rule/configs/global_config.yaml"
	if raw != want {
		t.Fatalf("URL = %q，期望 %q", raw, want)
	}
	if got, err := templateBucketKeyFromURL(raw, []string{templateManagedR2HostName}); err != nil || got != key {
		t.Fatalf("URL 反解 key = %q,%v", got, err)
	}
	if _, err := templateBucketKeyFromURL("https://evil.example.com/rule/configs/a.yaml", []string{templateManagedR2HostName}); err == nil {
		t.Fatal("外部 URL 应拒绝")
	}
}

func templateCreateRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestTemplateCreateWithBucketKey(t *testing.T) {
	fdb, db := newFakeDB(t)
	var insertArgs []any
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO config_templates") {
			insertArgs = fakeArgsAny(args)
		}
		return fakeResult(1, 9), nil
	}
	h := &handlers{db: db, log: testLogger(), cfg: config.Config{}}
	rec := httptest.NewRecorder()
	h.templateCreate(rec, templateCreateRequest(`{"name":"托管模板","bucket_key":"rule/configs/global_config.yaml"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["bucket_key"] != "rule/configs/global_config.yaml" ||
		!strings.Contains(mapStr(out, "url", ""), "/rule/configs/global_config.yaml") {
		t.Fatalf("create 响应异常：%#v", out)
	}
	if len(insertArgs) < 2 || insertArgs[1] != "https://s3.proxypilot.yangzifun.org/rule/configs/global_config.yaml" {
		t.Fatalf("INSERT 未存推导 URL：%#v", insertArgs)
	}

	// 外部 URL 拒绝。
	rec = httptest.NewRecorder()
	h.templateCreate(rec, templateCreateRequest(`{"name":"外部模板","url":"https://evil.example.com/t.yaml"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("外部 URL 应 400，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTemplatesListReturnsBucketKey(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT COUNT(*) FROM config_templates"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		case strings.Contains(q, "FROM config_templates"):
			return fakeRowsSet(
				[]string{"id", "name", "url", "description", "is_default", "created_at", "updated_at"},
				[]driver.Value{int64(1), "t", "https://s3.proxypilot.yangzifun.org/rule/configs/t.yaml", "", int64(1), int64(0), int64(0)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger(), cfg: config.Config{}}
	rec := httptest.NewRecorder()
	h.templatesList(rec, httptest.NewRequest(http.MethodGet, "/api/templates/manage?all=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bucket_key":"rule/configs/t.yaml"`) {
		t.Fatalf("列表缺少 bucket_key：%s", rec.Body.String())
	}
}

func TestTemplateContentSaveRequiresR2(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM config_templates WHERE id") {
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPut, "/api/templates/manage/7/content",
		strings.NewReader(`{"content":"port: 7890\n"}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templateContentSave(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "R2 未配置") {
		t.Fatalf("R2 未配置应明确 400：%d %s", rec.Code, rec.Body.String())
	}
}
func TestTemplateContentSaveBucketWriteFailure(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM config_templates WHERE id") {
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{R2Bucket: "proxypilot", R2Endpoint: "https://r2.example.com",
			R2AccessKeyID: "ak", R2SecretAccessKey: "sk"},
	}
	withTemplateStore(t, &fakeTemplateStore{putErr: errors.New("bucket down")})
	req := httptest.NewRequest(http.MethodPut, "/api/templates/manage/7/content",
		strings.NewReader(`{"content":"port: 7890\n"}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templateContentSave(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "写入存储桶失败") {
		t.Fatalf("写入失败应 502：%d %s", rec.Code, rec.Body.String())
	}
	if n := fdb.countExec("UPDATE config_templates"); n != 0 {
		t.Fatalf("写入失败不应同步 DB，实际 %d 次", n)
	}
}
