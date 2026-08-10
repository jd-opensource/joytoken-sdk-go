package joytoken

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.joytokens.ai"
	sdkVersion        = "0.2.0"
	maxSSETokenSize   = 16 * 1024 * 1024
	defaultTimeout    = 60 * time.Second
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
	apiKey           string
	apiBaseURL       string
	openAIBaseURL    string
	anthropicBaseURL string
	anthropicVersion string
	httpClient       HTTPClient
	defaultHeader    http.Header
	timeout          time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithAPIKey configures the API key used for authenticated requests.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey
	}
}

// WithAPIBaseURL configures the base URL for model and pricing endpoints.
func WithAPIBaseURL(apiBaseURL string) Option {
	return func(c *Client) {
		c.apiBaseURL = trimTrailingSlash(apiBaseURL)
	}
}

// WithOpenAIBaseURL configures the OpenAI-compatible API base URL.
func WithOpenAIBaseURL(openAIBaseURL string) Option {
	return func(c *Client) {
		c.openAIBaseURL = trimTrailingSlash(openAIBaseURL)
	}
}

// WithAnthropicBaseURL configures the Anthropic-compatible API base URL.
func WithAnthropicBaseURL(anthropicBaseURL string) Option {
	return func(c *Client) {
		c.anthropicBaseURL = trimTrailingSlash(anthropicBaseURL)
	}
}

// WithAnthropicVersion configures the anthropic-version request header.
func WithAnthropicVersion(anthropicVersion string) Option {
	return func(c *Client) {
		c.anthropicVersion = anthropicVersion
	}
}

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

// WithHeader adds a header to every request. Later calls replace the same key.
func WithHeader(key, value string) Option {
	return func(c *Client) {
		c.defaultHeader.Set(key, value)
	}
}

// NewClient creates a JoyToken client from environment defaults and options.
func NewClient(opts ...Option) *Client {
	apiBaseURL := getenv("JOY_TOKEN_API_BASE_URL", defaultAPIBaseURL)
	client := &Client{
		apiKey:           os.Getenv("JOY_TOKEN_API_KEY"),
		apiBaseURL:       trimTrailingSlash(apiBaseURL),
		openAIBaseURL:    trimTrailingSlash(getenv("JOY_TOKEN_OPENAI_BASE_URL", apiBaseURL+"/openai/v1")),
		anthropicBaseURL: trimTrailingSlash(getenv("JOY_TOKEN_ANTHROPIC_BASE_URL", apiBaseURL+"/anthropic/v1")),
		anthropicVersion: "2023-06-01",
		httpClient:       &http.Client{},
		defaultHeader:    make(http.Header),
		timeout:          defaultTimeout,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// CreateChatCompletion creates a non-streaming OpenAI-compatible completion.
func (c *Client) CreateChatCompletion(ctx context.Context, request ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = false
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
		body:    res.Body,
		scanner: newSSEScanner(res.Body),
		cancel:  cancel,
	}, nil
}

// CreateResponse creates a non-streaming OpenAI-compatible Responses result.
func (c *Client) CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = false
	var response Response
	if err := c.requestJSON(ctx, http.MethodPost, c.openAIBaseURL+"/responses", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// StreamResponse starts an OpenAI-compatible Responses SSE stream. The caller
// must close the returned stream. The stream ends after response.completed.
func (c *Client) StreamResponse(ctx context.Context, request ResponseRequest) (*ResponseStream, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = true
	requestCtx, cancel := c.withTimeout(ctx)
	req, err := c.newJSONRequest(requestCtx, http.MethodPost, c.openAIBaseURL+"/responses", request)
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

	return &ResponseStream{
		body:    res.Body,
		scanner: newSSEScanner(res.Body),
		cancel:  cancel,
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

// CreateMessage creates a non-streaming Anthropic-compatible message.
func (c *Client) CreateMessage(ctx context.Context, request MessageRequest) (*MessageResponse, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = false
	var response MessageResponse
	if err := c.requestAnthropicJSON(ctx, http.MethodPost, c.anthropicBaseURL+"/messages", request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// StreamMessage starts a streaming Anthropic-compatible message.
// The caller must close the returned stream.
func (c *Client) StreamMessage(ctx context.Context, request MessageRequest) (*MessageStream, error) {
	if err := validateAutoModel(request.Model); err != nil {
		return nil, err
	}
	if err := c.requireAPIKey(); err != nil {
		return nil, err
	}
	request.Stream = true
	requestCtx, cancel := c.withTimeout(ctx)
	req, err := c.newJSONRequest(requestCtx, http.MethodPost, c.anthropicBaseURL+"/messages", request)
	if err != nil {
		cancel()
		return nil, err
	}
	c.setAnthropicHeaders(req)
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

	return &MessageStream{
		body:    res.Body,
		scanner: newSSEScanner(res.Body),
		cancel:  cancel,
	}, nil
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
	req, err := c.newJSONRequest(requestCtx, method, url, body)
	if err != nil {
		return err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return parseAPIError(res)
	}

	return json.NewDecoder(res.Body).Decode(output)
}

func (c *Client) requestAnthropicJSON(ctx context.Context, method string, url string, body any, output any) error {
	requestCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	req, err := c.newJSONRequest(requestCtx, method, url, body)
	if err != nil {
		return err
	}
	c.setAnthropicHeaders(req)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return parseAPIError(res)
	}

	return json.NewDecoder(res.Body).Decode(output)
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

func (c *Client) setAnthropicHeaders(req *http.Request) {
	req.Header.Del("Authorization")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	req.Header.Set("anthropic-version", c.anthropicVersion)
}

// ChatCompletionStream reads Chat Completions SSE events.
type ChatCompletionStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	cancel  context.CancelFunc
}

// Recv returns the next completion chunk or io.EOF when the stream ends.
func (s *ChatCompletionStream) Recv() (*ChatCompletionChunk, error) {
	var chunk ChatCompletionChunk
	if err := recvSSEJSON(s.scanner, &chunk); err != nil {
		_ = s.Close()
		return nil, err
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

// ResponseStream reads OpenAI-compatible Responses SSE events.
type ResponseStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	cancel  context.CancelFunc
}

// Recv returns the next Responses event or io.EOF when the stream ends.
func (s *ResponseStream) Recv() (*ResponseStreamEvent, error) {
	var event ResponseStreamEvent
	if err := recvSSEJSON(s.scanner, &event); err != nil {
		_ = s.Close()
		return nil, err
	}
	return &event, nil
}

// Close closes the underlying streaming response body.
func (s *ResponseStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return s.body.Close()
}

// MessageStream reads Anthropic Messages SSE events.
type MessageStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	cancel  context.CancelFunc
}

// Recv returns the next message event or io.EOF when the stream ends.
func (s *MessageStream) Recv() (*MessageStreamEvent, error) {
	var event MessageStreamEvent
	if err := recvSSEJSON(s.scanner, &event); err != nil {
		_ = s.Close()
		return nil, err
	}
	return &event, nil
}

// Close closes the underlying streaming response body.
func (s *MessageStream) Close() error {
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

// APIError describes a non-successful JoyToken HTTP response.
type APIError struct {
	StatusCode      int
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
	return &APIError{
		StatusCode:      res.StatusCode,
		RequestID:       firstHeader(res.Header, "X-DAOE-Request-ID", "X-Request-ID"),
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
