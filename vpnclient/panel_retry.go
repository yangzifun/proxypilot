package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// panelDo 在弱网/跨境链路上对面板请求做有限重试。
//
// 实测：这条链路到 Cloudflare 的单次成功率并不稳定（1~4 秒，偶尔直接超时），
// 一次失败就放弃会让界面停在「加载不出配置」。这里只重试「传输层失败」，
// HTTP 4xx/5xx 不重试（那是业务错误，重试没意义）。
//
// newReq 每次都要构造一个全新的请求（body 不能复用）。
func panelDo(ctx context.Context, newReq func(context.Context) (*http.Request, error), attempts int) (*http.Response, error) {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := newReq(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := panelHTTP.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, err
		}
		// 退避：0.4s、1.2s
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(time.Duration(400*(i+1)) * time.Millisecond):
			}
		}
	}
	return nil, lastErr
}

// panelRequest 构造一次面板请求（body 每次重建）。
func panelRequest(ctx context.Context, method, url string, body []byte, token string) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}