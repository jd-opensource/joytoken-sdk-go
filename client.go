package joytoken

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.joytokens.ai"
	sdkVersion        = "0.2.0"
	maxSSETokenSize   = 16 * 1024 * 1024
	defaultTimeout    = 60 * time.Second

	// Model requests are non-idempotent and may already have reached the provider
	// when a transport or gateway error is observed. Keep automatic retries off by
	// default to avoid duplicate model calls, duplicate billing, and amplifying a
	// provider-side circuit breaker. Callers may opt in with WithMaxRetries when
	// their workflow supplies an appropriate idempotency strategy.
	defaultMaxRetries = 0
	// retryBaseDelay and retryMaxDelay bound the exponential backoff between
	// retries. Actual sleeps add jitter and honor a Retry-After response header
	// when the server sends one.
	retryBaseDelay = 500 * time.Millisecond
	retryMaxDelay  = 8 * time.Second
)

// ErrMissingAPIKey is returned when an authenticated endpoint is called
// without configuring a JoyToken API key.
var ErrMissingAPIKey = errors.New("joytoken API key is required; pass WithAPIKey or set JOY_TOKEN_API_KEY")

// HTTPClient is the subset of http.Client used by Client.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a reusable, concurrency-safe JoyToken API client.
type Client struct {
	apiKey        string
	apiBaseURL    string
	openAIBaseURL string
	httpClient    HTTPClient
	defaultHeader http.Header
	timeout       time.Duration

	// maxRetries is how many times a transient request failure (HTTP 429/5xx
	// or a transport error) is retried with exponential backoff before the
	// error is surfaced. Zero disables retries. Configured with WithMaxRetries.
	maxRetries int

	// tools holds handlers registered via WithTools/WithToolHandler. Supplying
	// any registered tool replaces the SDK default set. Primitive Create/Stream
	// methods forward user-owned tool calls; explicit Run methods execute them.
	tools     map[string]Tool
	toolOrder []string

	// defaultLocalTools controls the SDK fallback set used only when the caller
	// supplied no tools. Read/compute tools run locally; file_write and shell are
	// permission-gated. defaultBuiltinTools controls hosted Responses defaults.
	defaultLocalTools   bool
	defaultBuiltinTools bool

	// excludedDefaultTools names default tools (local or gateway built-in) to
	// skip when injecting the default set, keyed by tool/type name. It gives
	// per-tool opt-out on top of the coarse defaultLocalTools/defaultBuiltinTools
	// switches, e.g. dropping just "shell" while keeping the rest. It never
	// affects tools the caller registered explicitly via WithTools. Configured
	// with WithoutDefaultTools.
	excludedDefaultTools map[string]bool

	// fileWorkspaceRoot is the sandbox root for the default file tools. Empty
	// means the current working directory (os.Getwd), which most closely
	// matches the Codex/Claude "project workspace" model and keeps the exposed
	// surface minimal. It can be overridden with WithFileWorkspace.
	fileWorkspaceRoot string

	// filePermission, when non-nil, allows the default file_write tool to run.
	// Read and exploration tools (file_search, list_dir, file_read) are always
	// available, and file_write is always declared to the model too; writes have
	// real side effects, so every write is gated. With no callback (this field
	// nil), the file_write declaration is still sent but writes are refused at
	// execution time. Supply the callback via WithFilePermission to allow writes.
	filePermission FilePermissionFunc

	// shellWorkingDir is the directory the default shell tool runs commands in.
	// Empty means the current working directory (os.Getwd), matching the file
	// tools' project-workspace model. It can be overridden with WithShellWorkspace.
	shellWorkingDir string

	// shellPermission, when non-nil, allows the default shell tool to run. The
	// shell tool is always declared to the model, but because a shell command has
	// unbounded side effects every invocation is gated. With no callback (this
	// field nil), the shell declaration is still sent but commands are refused at
	// execution time. Supply the callback via WithShellPermission to allow commands.
	shellPermission ShellPermissionFunc
}

// Option configures a Client.
type Option func(*Client)

// WithAPIKey configures the API key used for authenticated requests.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey
	}
}

// WithAPIBaseURL configures the common JoyToken base URL. Because the gateway
// has one model entry point, it also derives openAIBaseURL as
// <base>/openai/v1. A later WithOpenAIBaseURL call may override it explicitly.
func WithAPIBaseURL(apiBaseURL string) Option {
	return func(c *Client) {
		baseURL := trimTrailingSlash(apiBaseURL)
		c.apiBaseURL = baseURL
		c.openAIBaseURL = baseURL + "/openai/v1"
	}
}

// WithOpenAIBaseURL configures the OpenAI-compatible API base URL.
func WithOpenAIBaseURL(openAIBaseURL string) Option {
	return func(c *Client) {
		c.openAIBaseURL = trimTrailingSlash(openAIBaseURL)
	}
}

// WithAnthropicBaseURL is retained for source compatibility. Anthropic
// Messages is now a local adapter over the gateway's single Chat Completions
// endpoint, so there is no separate Anthropic URL to configure.
// Deprecated: configure WithOpenAIBaseURL or WithAPIBaseURL instead.
func WithAnthropicBaseURL(_ string) Option { return func(*Client) {} }

// WithAnthropicVersion is retained for source compatibility. No Anthropic HTTP
// request is emitted by this SDK adapter.
// Deprecated: the value is ignored.
func WithAnthropicVersion(_ string) Option { return func(*Client) {} }

// WithHTTPClient configures the HTTP transport used by the client.
func WithHTTPClient(httpClient HTTPClient) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithTimeout configures the maximum duration for a request, including reading
// a non-streaming response or consuming a streaming response. A non-positive
// duration disables the SDK timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithMaxRetries configures how many times a transient request failure is
// retried before the error is returned. Transient failures are HTTP 429 and
// 5xx responses and low-level transport errors; 4xx responses (except 429) are
// never retried because they will not succeed on replay. Retries use bounded
// exponential backoff with jitter and honor a Retry-After response header when
// present. Model requests are not inherently idempotent, so retries are disabled
// by default and should be enabled only when the caller accepts that risk. A
// negative value is treated as zero.
func WithMaxRetries(maxRetries int) Option {
	return func(c *Client) {
		if maxRetries < 0 {
			maxRetries = 0
		}
		c.maxRetries = maxRetries
	}
}

// WithHeader adds a header to every request. Later calls replace the same key.
func WithHeader(key, value string) Option {
	return func(c *Client) {
		c.defaultHeader.Set(key, value)
	}
}

// WithTools registers executable tools on the client. A non-empty registered
// set replaces the SDK defaults. Primitive Create/Stream methods only expose
// and forward these user-owned tools; explicit Run methods execute them. Later
// registrations with the same name replace earlier ones while preserving
// first-seen ordering.
func WithTools(tools ...Tool) Option {
	return func(c *Client) {
		for _, t := range tools {
			c.registerTool(t)
		}
	}
}

// WithToolHandler registers a single named executable tool. It is a convenience
// wrapper over WithTools for the common case of attaching one handler.
func WithToolHandler(name, description string, parameters map[string]any, execute ToolExecuteFunc) Option {
	return WithTools(Tool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
		Execute:     execute,
	})
}

// WithDefaultLocalTools controls the SDK fallback tool set used when the caller
// supplies no request-level or registered tools. It includes compute/read tools
// plus permission-gated file_write and shell. Pass false to disable it.
func WithDefaultLocalTools(enabled bool) Option {
	return func(c *Client) {
		c.defaultLocalTools = enabled
	}
}

// WithDefaultBuiltinTools controls whether zero-config hosted Responses tools
// are sent when the caller supplied no tools. It is disabled by default because
// hosted tools may carry separate provider availability and billing semantics.
// Currently web_search_preview is the only opt-in hosted default; file_search
// still requires caller-provided vector_store_ids. Explicit request tools are
// always forwarded unchanged.
func WithDefaultBuiltinTools(enabled bool) Option {
	return func(c *Client) {
		c.defaultBuiltinTools = enabled
	}
}

// WithoutDefaultTools excludes specific default tools by name, giving per-tool
// opt-out on top of the coarse WithDefaultLocalTools/WithDefaultBuiltinTools
// switches. Names match the tool's declared name for local tools (e.g.
// "shell", "file_write", "calculator") and the Type for gateway built-in tools
// (e.g. "web_search_preview"). Excluded tools are neither declared nor executed.
// It never affects tools the caller registered explicitly via WithTools/
// WithToolHandler; those always win. Calling it multiple times accumulates.
func WithoutDefaultTools(names ...string) Option {
	return func(c *Client) {
		if c.excludedDefaultTools == nil {
			c.excludedDefaultTools = make(map[string]bool, len(names))
		}
		for _, n := range names {
			if n != "" {
				c.excludedDefaultTools[n] = true
			}
		}
	}
}

// NewClient creates a JoyToken client from environment defaults and options.
func NewClient(opts ...Option) *Client {
	apiBaseURL := getenv("JOY_TOKEN_API_BASE_URL", defaultAPIBaseURL)
	client := &Client{
		apiKey:        os.Getenv("JOY_TOKEN_API_KEY"),
		apiBaseURL:    trimTrailingSlash(apiBaseURL),
		openAIBaseURL: trimTrailingSlash(getenv("JOY_TOKEN_OPENAI_BASE_URL", apiBaseURL+"/openai/v1")),
		httpClient:    &http.Client{},
		defaultHeader: make(http.Header),
		timeout:       defaultTimeout,
		maxRetries:    defaultMaxRetries,

		// Local fallbacks are enabled out of the box but selected only when the
		// caller supplied no request-level or registered tools. Hosted Responses
		// tools are opt-in because the selected Chat upstream may not support them.
		defaultLocalTools:   true,
		defaultBuiltinTools: false,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// CreateChatCompletion sends an OpenAI-compatible Chat Completions request to
// the gateway's single model endpoint. Request-level tools, including an
// explicitly empty slice, are forwarded unchanged. Tools registered with
// WithTools are also user-owned and are forwarded without automatic execution.
// Only when the caller supplied no tools at either level does the client inject
// and execute its default fallback tools.
func (c *Client) CreateChatCompletion(ctx context.Context, request ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if request.Tools != nil || len(c.toolOrder) > 0 {
		return c.createChatCompletionOnce(ctx, request)
	}
	run, err := c.runChatCompletion(ctx, request, RunChatOptions{}, nil)
	if err != nil {
		return nil, err
	}
	return run.finalResponse(nil), nil
}

// createChatCompletionOnce performs exactly one non-streaming Chat Completions
// round-trip with the effective tools injected. It never executes tool_calls,
// so the RunChatCompletion loop uses it as its single-shot primitive without
// recursing back into the auto-executing CreateChatCompletion.
func (c *Client) createChatCompletionOnce(ctx context.Context, request ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = false
	// Resolve tools once for this wire request. Request-level declarations are
	// copied unchanged; registered tools replace the defaults; defaults are used
	// only when neither source was supplied. Tool declarations remain available
	// on continuation turns so the model can make another tool call, and so an
	// external caller's explicit tools are never silently removed.
	request.Tools = c.chatTools(request)
	var response ChatCompletionResponse
	if err := c.requestJSON(ctx, http.MethodPost, c.openAIBaseURL+"/chat/completions", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// StreamChatCompletion starts a streaming OpenAI-compatible completion.
// The caller must close the returned stream.
func (c *Client) StreamChatCompletion(ctx context.Context, request ChatCompletionRequest) (*ChatCompletionStream, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = true
	request.Tools = c.chatTools(request)
	requestCtx, cancel := c.withTimeout(ctx)
	req, err := c.newJSONRequest(requestCtx, http.MethodPost, c.openAIBaseURL+"/chat/completions", request)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	res, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		cancel()
		return nil, parseAPIError(res)
	}

	return &ChatCompletionStream{
		body:      res.Body,
		scanner:   newSSEScanner(res.Body),
		cancel:    cancel,
		requestID: requestIDFromHeaders(res.Header),
	}, nil
}

// GenerateImage creates an OpenAI-compatible image generation.
func (c *Client) GenerateImage(ctx context.Context, request ImageGenerationRequest) (*ImageGenerationResponse, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	var response ImageGenerationResponse
	if err := c.requestJSON(ctx, http.MethodPost, c.openAIBaseURL+"/images/generations", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ListModels lists the public JoyToken model catalog.
func (c *Client) ListModels(ctx context.Context) (*ModelListResponse, error) {
	return c.ListModelsWithOptions(ctx, ListModelsOptions{})
}

// ListModelsWithOptions lists the public JoyToken model catalog with optional
// response localization. When Locale is empty, the API defaults to English.
func (c *Client) ListModelsWithOptions(ctx context.Context, options ListModelsOptions) (*ModelListResponse, error) {
	endpoint := c.apiBaseURL + "/api/v1/models"
	if options.Locale != "" {
		if options.Locale != ModelLocaleZH && options.Locale != ModelLocaleEN {
			return nil, fmt.Errorf("joytoken: model locale must be %q or %q", ModelLocaleZH, ModelLocaleEN)
		}
		endpoint += "?locale=" + url.QueryEscape(string(options.Locale))
	}

	var response struct {
		Code    int             `json:"code,omitempty"`
		Message string          `json:"message,omitempty"`
		Object  string          `json:"object,omitempty"`
		Data    json.RawMessage `json:"data"`
	}
	if err := c.requestJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return nil, err
	}

	var models []ModelInfo
	data := bytes.TrimSpace(response.Data)
	switch {
	case len(data) == 0 || bytes.Equal(data, []byte("null")):
	case data[0] == '[':
		if err := json.Unmarshal(data, &models); err != nil {
			return nil, fmt.Errorf("joytoken: decode model list: %w", err)
		}
	case data[0] == '{':
		var catalog struct {
			Models []ModelInfo `json:"models"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			return nil, fmt.Errorf("joytoken: decode model list: %w", err)
		}
		models = catalog.Models
	default:
		return nil, fmt.Errorf("joytoken: unexpected model list response")
	}

	return &ModelListResponse{
		Code:    response.Code,
		Message: response.Message,
		Object:  response.Object,
		Data:    ModelListData{Models: models},
	}, nil
}

// GetModelMeta returns filter metadata for the model catalog.
func (c *Client) GetModelMeta(ctx context.Context) (*ModelMetadataResponse, error) {
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	var response ModelMetadataResponse
	if err := c.requestJSON(ctx, http.MethodGet, c.apiBaseURL+"/api/v1/models/meta", nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetPricing returns the current JoyToken pricing catalog.
func (c *Client) GetPricing(ctx context.Context) (*PricingResponse, error) {
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	var response PricingResponse
	if err := c.requestJSON(ctx, http.MethodGet, c.apiBaseURL+"/api/v1/pricing", nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) requestJSON(ctx context.Context, method string, url string, body any, output any) error {
	requestCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	res, err := c.sendWithRetry(requestCtx, method, url, body, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return parseAPIError(res)
	}

	if err := json.NewDecoder(res.Body).Decode(output); err != nil {
		return err
	}
	normalizeSuccessOutput(output, res.Header)
	return nil
}

// sendWithRetry issues the request and transparently retries transient
// failures (HTTP 429/5xx and transport errors) up to c.maxRetries times with
// bounded exponential backoff and jitter, honoring a Retry-After header when
// present. The request body is rebuilt on every attempt (newJSONRequest
// re-marshals it), but rebuilding the body does not make a model invocation
// idempotent; callers must explicitly opt in through WithMaxRetries. The prepare
// hook, if set, mutates the request after the standard headers are applied.
// A non-retryable response (2xx or a 4xx other than 429) is returned as-is for
// the caller to decode or turn into an APIError.
func (c *Client) sendWithRetry(ctx context.Context, method, url string, body any, prepare func(*http.Request)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := c.newJSONRequest(ctx, method, url, body)
		if err != nil {
			return nil, err
		}
		if prepare != nil {
			prepare(req)
		}

		res, err := c.httpClient.Do(req)
		if err != nil {
			// Never retry a canceled/expired context; surface it immediately.
			if ctx.Err() != nil {
				return nil, err
			}
			if attempt >= c.maxRetries {
				return nil, err
			}
			if !c.sleepBeforeRetry(ctx, attempt, 0) {
				return nil, err
			}
			continue
		}

		if attempt < c.maxRetries && isRetryableStatus(res.StatusCode) {
			retryAfter := parseRetryAfter(res.Header.Get("Retry-After"))
			// Drain and close so the underlying connection can be reused.
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if !c.sleepBeforeRetry(ctx, attempt, retryAfter) {
				return nil, ctx.Err()
			}
			continue
		}

		return res, nil
	}
}

// isRetryableStatus reports whether an HTTP status warrants an automatic retry:
// 429 (rate limited) and 5xx (server/gateway errors, including the transient
// 503 upstream blip). 4xx other than 429 are deterministic client errors and
// are never retried.
func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
}

// sleepBeforeRetry waits for the backoff interval for the given attempt (0-based)
// before the next try. It prefers an explicit Retry-After duration when the
// server provided one, otherwise uses exponential backoff (retryBaseDelay * 2^attempt)
// capped at retryMaxDelay with full jitter. It returns false if ctx is done
// while waiting so the caller can abort.
func (c *Client) sleepBeforeRetry(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	delay := retryAfter
	if delay <= 0 {
		backoff := retryBaseDelay << attempt
		if backoff > retryMaxDelay || backoff <= 0 {
			backoff = retryMaxDelay
		}
		// Full jitter: sleep a random duration in [0, backoff].
		delay = time.Duration(rand.Int63n(int64(backoff) + 1))
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// parseRetryAfter interprets a Retry-After header value, which may be either a
// number of seconds or an HTTP date. It returns 0 when the value is absent or
// unparseable, in which case the caller falls back to exponential backoff.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) newJSONRequest(ctx context.Context, method string, url string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}

	for key, values := range c.defaultHeader {
		if len(values) > 0 {
			req.Header.Set(key, values[len(values)-1])
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "joytoken-sdk-go/"+sdkVersion)
	}
	req.Header.Del("x-api-key")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	return req, nil
}

// ChatCompletionStream reads Chat Completions SSE events.
type ChatCompletionStream struct {
	body      io.ReadCloser
	scanner   *bufio.Scanner
	cancel    context.CancelFunc
	requestID string
}

// Recv returns the next completion chunk or io.EOF when the stream ends.
func (s *ChatCompletionStream) Recv() (*ChatCompletionChunk, error) {
	var chunk ChatCompletionChunk
	if err := recvSSEJSON(s.scanner, &chunk); err != nil {
		_ = s.Close()
		return nil, err
	}
	normalizeChatChunk(&chunk, s.requestID)
	if requestID := chunk.RequestID(); requestID != "" {
		s.requestID = requestID
	}
	return &chunk, nil
}

// Close closes the underlying streaming response body.
func (s *ChatCompletionStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return s.body.Close()
}

func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *Client) requireAPIKey() error {
	if strings.TrimSpace(c.apiKey) == "" {
		return ErrMissingAPIKey
	}
	return nil
}

func validateAutoModel(model string) error {
	if model != ModelAuto {
		return fmt.Errorf("joytoken: model must be %q", ModelAuto)
	}
	return nil
}

// ErrorCode is a provider-neutral classification of a failed request. It is
// aligned with the JoyToken TypeScript SDK so callers get identical error
// semantics across languages.
type ErrorCode string

const (
	ErrorCodeRateLimited    ErrorCode = "rate_limited"
	ErrorCodeServerError    ErrorCode = "server_error"
	ErrorCodeTimeout        ErrorCode = "timeout"
	ErrorCodeNetwork        ErrorCode = "network"
	ErrorCodeInvalidRequest ErrorCode = "invalid_request"
	ErrorCodeAuthentication ErrorCode = "authentication"
	ErrorCodePermission     ErrorCode = "permission"
	ErrorCodeNotFound       ErrorCode = "not_found"
	ErrorCodeUnknown        ErrorCode = "unknown"
)

// classifyStatus maps an HTTP status code to a provider-neutral ErrorCode.
func classifyStatus(status int) ErrorCode {
	switch {
	case status == http.StatusTooManyRequests:
		return ErrorCodeRateLimited
	case status == http.StatusUnauthorized:
		return ErrorCodeAuthentication
	case status == http.StatusForbidden:
		return ErrorCodePermission
	case status == http.StatusNotFound:
		return ErrorCodeNotFound
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ErrorCodeInvalidRequest
	case status >= 500 && status <= 599:
		return ErrorCodeServerError
	default:
		return ErrorCodeUnknown
	}
}

// APIError describes a non-successful JoyToken HTTP response.
type APIError struct {
	StatusCode      int
	Code            ErrorCode
	RequestID       string
	ResponseHeaders http.Header
	Body            any
}

// Error returns a readable API failure description.
func (e *APIError) Error() string {
	if e.Body == nil {
		return fmt.Sprintf("joytoken api request failed with status %d", e.StatusCode)
	}
	if body, ok := e.Body.(string); ok {
		return fmt.Sprintf("joytoken api request failed with status %d: %s", e.StatusCode, body)
	}
	body, err := json.Marshal(e.Body)
	if err != nil {
		return fmt.Sprintf("joytoken api request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("joytoken api request failed with status %d: %s", e.StatusCode, body)
}

// IsAPIError reports whether err contains an APIError.
func IsAPIError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr)
}

func parseAPIError(res *http.Response) error {
	raw, _ := io.ReadAll(res.Body)
	var body any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			body = string(raw)
		}
	}
	requestID := requestIDFromHeaders(res.Header)
	if requestID == "" {
		requestID = requestIDFromBody(body)
	}
	return &APIError{
		StatusCode:      res.StatusCode,
		Code:            classifyStatus(res.StatusCode),
		RequestID:       requestID,
		ResponseHeaders: res.Header.Clone(),
		Body:            body,
	}
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func trimTrailingSlash(value string) string {
	return strings.TrimRight(value, "/")
}

func firstHeader(headers http.Header, keys ...string) string {
	for _, key := range keys {
		if value := headers.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func newSSEScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxSSETokenSize)
	return scanner
}

func recvSSEJSON(scanner *bufio.Scanner, output any) error {
	dataLines := make([]string, 0, 1)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			return decodeSSEJSON(dataLines, output)
		}
		if strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		dataLines = append(dataLines, strings.TrimPrefix(data, " "))
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if len(dataLines) > 0 {
		return decodeSSEJSON(dataLines, output)
	}
	return io.EOF
}

func decodeSSEJSON(dataLines []string, output any) error {
	data := strings.Join(dataLines, "\n")
	if strings.TrimSpace(data) == "[DONE]" {
		return io.EOF
	}
	return json.Unmarshal([]byte(data), output)
}
