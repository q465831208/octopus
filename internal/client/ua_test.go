package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestUAInjection 验证出站请求默认注入浏览器 UA（规避 Cloudflare 对机器人 UA 的 403）。
func TestUAInjection(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got != model.DefaultUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, model.DefaultUserAgent)
	}
}

// TestUAInjectionKeepsCustom 验证自定义 UA 不会被覆盖。
func TestUAInjectionKeepsCustom(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "MyCustom/1.0")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got != "MyCustom/1.0" {
		t.Fatalf("User-Agent = %q, want custom preserved", got)
	}
}