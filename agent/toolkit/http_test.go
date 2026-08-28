package toolkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

func newTestServer(t *testing.T, body string) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return server, parsed.Hostname()
}

func TestHTTPFetchAllowedHost(t *testing.T) {
	server, host := newTestServer(t, "pong")
	tool := HTTPFetch(HTTPFetchConfig{AllowedHosts: []string{host}, Client: server.Client()})
	out, err := tool.Execute(context.Background(), map[string]any{"url": server.URL}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)
	if result["body"].(string) != "pong" {
		t.Fatalf("expected body \"pong\", got %q", result["body"])
	}
	if result["status"].(int) != 200 {
		t.Fatalf("expected status 200, got %v", result["status"])
	}
}

func TestHTTPFetchRejectsDisallowedHost(t *testing.T) {
	server, _ := newTestServer(t, "pong")
	tool := HTTPFetch(HTTPFetchConfig{AllowedHosts: []string{"example.com"}, Client: server.Client()})
	if _, err := tool.Execute(context.Background(), map[string]any{"url": server.URL}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected disallowed host to be rejected")
	}
}

func TestHTTPFetchEmptyAllowlistDeniesAll(t *testing.T) {
	server, _ := newTestServer(t, "pong")
	tool := HTTPFetch(HTTPFetchConfig{Client: server.Client()})
	if _, err := tool.Execute(context.Background(), map[string]any{"url": server.URL}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected empty allowlist to deny all")
	}
}

func TestHTTPFetchRejectsNonHTTPScheme(t *testing.T) {
	tool := HTTPFetch(HTTPFetchConfig{AllowedHosts: []string{"localhost"}})
	if _, err := tool.Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"}, agent.ToolExecutionContext{}); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestHTTPFetchTruncatesLargeBody(t *testing.T) {
	server, host := newTestServer(t, "abcdefghij")
	tool := HTTPFetch(HTTPFetchConfig{AllowedHosts: []string{host}, MaxBytes: 4, Client: server.Client()})
	out, err := tool.Execute(context.Background(), map[string]any{"url": server.URL}, agent.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)
	if result["bytes"].(int) != 4 {
		t.Fatalf("expected 4 bytes, got %v", result["bytes"])
	}
	if result["truncated"].(bool) != true {
		t.Fatal("expected truncated flag to be true")
	}
}
