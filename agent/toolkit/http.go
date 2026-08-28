package toolkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// HTTPFetchConfig configures the local HTTP fetch fallback tool. This tool is a
// local safety net for gateways that do not passthrough web fetching; when the
// gateway forwards http_fetch to the vendor, prefer that instead.
//
// AllowedHosts is an allowlist of hostnames the model may fetch. It is
// mandatory: an empty allowlist denies every request, which prevents SSRF by
// default. Timeout, MaxBytes and Client all have safe zero-value defaults.
type HTTPFetchConfig struct {
	// AllowedHosts lists hostnames (exact match, case-insensitive) the tool may
	// reach. Empty means deny all.
	AllowedHosts []string
	// Timeout bounds a single request. Zero means DefaultHTTPTimeout.
	Timeout time.Duration
	// MaxBytes caps the response body read into memory. Zero means
	// DefaultHTTPMaxBytes.
	MaxBytes int64
	// Client is the HTTP client used for requests. Nil means a client built
	// from Timeout.
	Client *http.Client
}

// Defaults for the HTTP fetch tool.
const (
	DefaultHTTPTimeout        = 15 * time.Second
	DefaultHTTPMaxBytes int64 = 1 << 20 // 1 MiB
)

// HTTPFetch returns a local, read-only HTTP GET tool constrained to an explicit
// host allowlist. It is side-effect free but reaches the network, so register
// it under PermissionAuto only when the allowlist is trusted; otherwise use
// PermissionAsk.
func HTTPFetch(config HTTPFetchConfig) agent.AgentTool {
	allowed := make(map[string]struct{}, len(config.AllowedHosts))
	for _, host := range config.AllowedHosts {
		allowed[strings.ToLower(strings.TrimSpace(host))] = struct{}{}
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}
	maxBytes := config.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultHTTPMaxBytes
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return agent.AgentTool{
		Name:        "http_fetch",
		Description: "Fetch the text body of an HTTP(S) URL via GET. Only hosts on the configured allowlist are permitted.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Absolute http:// or https:// URL to fetch.",
				},
			},
			"required": []string{"url"},
		},
		Execute: func(ctx context.Context, input any, _ agent.ToolExecutionContext) (any, error) {
			raw, err := stringArg(input, "url")
			if err != nil {
				return nil, err
			}
			parsed, err := validateURL(raw, allowed)
			if err != nil {
				return nil, fmt.Errorf("http_fetch: %w", err)
			}

			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("http_fetch: %w", err)
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, fmt.Errorf("http_fetch: %w", err)
			}
			defer response.Body.Close()

			body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes))
			if err != nil {
				return nil, fmt.Errorf("http_fetch: %w", err)
			}
			return map[string]any{
				"status":       response.StatusCode,
				"content_type": response.Header.Get("Content-Type"),
				"body":         string(body),
				"bytes":        len(body),
				"truncated":    int64(len(body)) >= maxBytes,
			}, nil
		},
	}
}

// validateURL parses the URL, enforces the http(s) scheme, and checks the host
// against the allowlist. An empty allowlist denies everything.
//
// Security note: this check operates on the hostname string, not on the IP the
// host ultimately resolves to. It does NOT defend against an allowlisted host
// whose DNS record points at a private/link-local range (e.g. a name that
// resolves to 127.0.0.1, 169.254.0.0/16, or 10.0.0.0/8), nor against
// DNS-rebinding. Only allowlist hosts you fully control; if you must fetch
// untrusted hosts, add a resolve-then-verify step that rejects private,
// loopback, and link-local IPs (and re-checks after the connection is made).
func validateURL(raw string, allowed map[string]struct{}) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http and https URLs are allowed, got %q", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("host %q is not allowed (allowlist is empty)", host)
	}
	if _, ok := allowed[host]; !ok {
		return nil, fmt.Errorf("host %q is not on the allowlist", host)
	}
	return parsed, nil
}
