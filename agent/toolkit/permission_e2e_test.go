package toolkit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// newToolCallGateway builds a mock JoyToken Gateway that drives the full agent
// loop: the first chat completion returns a tool_call for toolName, every
// later completion returns finalText as a plain assistant message. This lets a
// test exercise the seam the existing suites never cross — a tool_call coming
// back over HTTP, flowing through the agent loop, and hitting the toolkit
// permission gate. It also counts how many completions were requested.
func newToolCallGateway(t *testing.T, toolName, finalText string, calls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		n := atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      toolName,
							"arguments": `{"expression":"1+1"}`,
						},
					}},
				}}},
				"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": finalText}}},
			"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
		})
	}))
}

// newAgent wires a real Client -> JoyTokenProvider -> agent.New, with tools
// taken from the given toolkit so the permission middleware is in the loop.
func newAgent(t *testing.T, tk *Toolkit, baseURL string) *agent.Agent {
	t.Helper()
	client := joytoken.NewClient(
		joytoken.WithAPIKey("test-key"),
		joytoken.WithOpenAIBaseURL(baseURL+"/openai/v1"),
		// Turn off the client's own default-tool auto-execution loop so a
		// tool_call coming back over HTTP is passed through to the agent,
		// where the toolkit permission gate lives, instead of being executed
		// inside the client (which has no permission gate). Without this the
		// client would silently run calculator and return final text, and the
		// agent would never see the tool_call.
		joytoken.WithDefaultLocalTools(false),
	)
	return agent.New(agent.AgentOptions{
		Model: agent.NewJoyTokenProvider(client),
		Tools: tk.Tools(),
	})
}

// TestE2EPermissionDenyFedBackAsError verifies that a Deny policy blocks the
// tool over the full loop and the "denied" error is fed back to the model as
// an observation, letting the run finish with the second-turn text rather than
// aborting.
func TestE2EPermissionDenyFedBackAsError(t *testing.T) {
	var calls int32
	server := newToolCallGateway(t, "calculator", "done", &calls)
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionDeny})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Run returned error, expected denial fed back: %v", err)
	}
	if result.FinalText != "done" {
		t.Fatalf("expected run to finish after denial, got %q", result.FinalText)
	}
	first := result.Steps[0].ToolResults[0]
	if !first.IsError || !strings.Contains(first.Content, "denied") {
		t.Fatalf("expected denied error fed back, got %#v", first)
	}
}

// TestE2EPermissionAskRejectedFedBack verifies that an Ask handler returning
// false rejects the tool and the rejection is fed back as an error over the
// full loop.
func TestE2EPermissionAskRejectedFedBack(t *testing.T) {
	var calls int32
	server := newToolCallGateway(t, "calculator", "done", &calls)
	defer server.Close()

	askCalls := 0
	ask := func(context.Context, PermissionRequest) (bool, error) {
		askCalls++
		return false, nil
	}
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Run returned error, expected rejection fed back: %v", err)
	}
	if askCalls != 1 {
		t.Fatalf("expected ask handler called once, got %d", askCalls)
	}
	first := result.Steps[0].ToolResults[0]
	if !first.IsError || !strings.Contains(first.Content, "rejected") {
		t.Fatalf("expected rejected error fed back, got %#v", first)
	}
}

// TestE2EPermissionAskApproved verifies that an Ask handler returning true lets
// the tool run over the full loop and the run finishes with real tool output.
func TestE2EPermissionAskApproved(t *testing.T) {
	var calls int32
	server := newToolCallGateway(t, "calculator", "final", &calls)
	defer server.Close()

	askCalls := 0
	ask := func(context.Context, PermissionRequest) (bool, error) {
		askCalls++
		return true, nil
	}
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if askCalls != 1 {
		t.Fatalf("expected ask handler called once, got %d", askCalls)
	}
	first := result.Steps[0].ToolResults[0]
	if first.IsError {
		t.Fatalf("expected approved tool to succeed, got %#v", first)
	}
	if result.FinalText != "final" {
		t.Fatalf("expected final text, got %q", result.FinalText)
	}
}

// TestE2EPermissionAskNoHandlerFailsSafe verifies the fail-safe path over the
// full loop: Ask mode with a nil handler denies the tool and feeds the error
// back rather than silently allowing it.
func TestE2EPermissionAskNoHandlerFailsSafe(t *testing.T) {
	var calls int32
	server := newToolCallGateway(t, "calculator", "done", &calls)
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionAsk})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Run returned error, expected fail-safe fed back: %v", err)
	}
	first := result.Steps[0].ToolResults[0]
	if !first.IsError || !strings.Contains(first.Content, "no permission handler") {
		t.Fatalf("expected fail-safe error fed back, got %#v", first)
	}
}

// TestE2EPermissionAskCalledEachToolCall verifies the Ask handler is consulted
// on every tool call across multiple turns, and the step count advances.
func TestE2EPermissionAskCalledEachToolCall(t *testing.T) {
	var completions int32
	// Gateway returns a tool_call on the first two turns, plain text on the third.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&completions, 1)
		w.Header().Set("Content-Type", "application/json")
		if n <= 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call", "type": "function",
						"function": map[string]any{"name": "calculator", "arguments": `{"expression":"1+1"}`},
					}},
				}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "done"}}},
		})
	}))
	defer server.Close()

	askCalls := 0
	ask := func(context.Context, PermissionRequest) (bool, error) {
		askCalls++
		return true, nil
	}
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if askCalls != 2 {
		t.Fatalf("expected ask handler called twice, got %d", askCalls)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(result.Steps))
	}
	if result.FinalText != "done" {
		t.Fatalf("expected final text 'done', got %q", result.FinalText)
	}
}

// TestE2ECalculatorOutputFedBackIntoContext closes the gap the other suites
// leave open: they only assert a tool ran without error, never that it produced
// the *correct* value or that the value flowed back into the model's context.
// Here the gateway computes calculator's real output on the first turn and, on
// the second turn, captures the request body to prove the "2" observation was
// fed back as a tool message before the model produced its final answer.
func TestE2ECalculatorOutputFedBackIntoContext(t *testing.T) {
	var calls int32
	var secondTurnBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "calculator", "arguments": `{"expression":"1+1"}`},
					}},
				}}},
			})
			return
		}
		buf := new(strings.Builder)
		_, _ = io.Copy(buf, r.Body)
		secondTurnBody = buf.String()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "the answer is 2"}}},
		})
	}))
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionAuto})).Register(Calculator())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute 1+1")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// 1. The tool actually computed the right value (not just "no error").
	first := result.Steps[0].ToolResults[0]
	if first.IsError {
		t.Fatalf("expected calculator to succeed, got %#v", first)
	}
	if !strings.Contains(first.Content, "2") {
		t.Fatalf("expected calculator output to contain 2, got %q", first.Content)
	}

	// 2. That real output was fed back into the model's context on turn two.
	if !strings.Contains(secondTurnBody, `"role":"tool"`) {
		t.Fatalf("expected a tool observation in the second-turn request, got: %s", secondTurnBody)
	}
	if !strings.Contains(secondTurnBody, "2") {
		t.Fatalf("expected the computed value 2 fed back in the second-turn request, got: %s", secondTurnBody)
	}
	if result.FinalText != "the answer is 2" {
		t.Fatalf("expected final answer using the tool result, got %q", result.FinalText)
	}
}

// TestE2EDateTimeToolFullLoop runs a second, differently-shaped tool (datetime)
// through the same permission + agent loop, closing the coverage gap where only
// calculator was ever exercised end-to-end. It asserts the tool's real output
// (the resolved timezone) makes it into the tool result and back into context.
func TestE2EDateTimeToolFullLoop(t *testing.T) {
	var calls int32
	var secondTurnBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call_dt", "type": "function",
						"function": map[string]any{"name": "datetime", "arguments": `{"timezone":"Asia/Shanghai"}`},
					}},
				}}},
			})
			return
		}
		buf := new(strings.Builder)
		_, _ = io.Copy(buf, r.Body)
		secondTurnBody = buf.String()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "reported"}}},
		})
	}))
	defer server.Close()

	tk := New(WithPermission(Permission{Mode: PermissionAuto})).Register(DateTime())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "what time is it in Shanghai")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	first := result.Steps[0].ToolResults[0]
	if first.IsError {
		t.Fatalf("expected datetime to succeed, got %#v", first)
	}
	// datetime resolves the requested timezone into its output; assert the
	// real resolved value, not just absence of error.
	if !strings.Contains(first.Content, "Asia/Shanghai") {
		t.Fatalf("expected datetime output to carry the resolved timezone, got %q", first.Content)
	}
	if !strings.Contains(secondTurnBody, "Asia/Shanghai") {
		t.Fatalf("expected datetime output fed back into the second-turn request, got: %s", secondTurnBody)
	}
	if result.FinalText != "reported" {
		t.Fatalf("expected final text after datetime loop, got %q", result.FinalText)
	}
}

// TestE2EMultipleToolCallsInOneTurn closes a gap every other suite leaves open:
// they all return exactly one tool_call per turn, so nothing proves the agent
// handles a single assistant message that requests *several* tools at once —
// the shape a model really emits when it wants to run tools in parallel. Here
// the gateway returns two tool_calls (calculator + datetime) in the first turn.
// The agent must execute both, consult the Ask gate once per call (twice), feed
// both observations back before the second turn, and preserve call order.
func TestE2EMultipleToolCallsInOneTurn(t *testing.T) {
	var calls int32
	var secondTurnBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id": "call_calc", "type": "function",
							"function": map[string]any{"name": "calculator", "arguments": `{"expression":"1+1"}`},
						},
						map[string]any{
							"id": "call_dt", "type": "function",
							"function": map[string]any{"name": "datetime", "arguments": `{"timezone":"Asia/Shanghai"}`},
						},
					},
				}}},
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		secondTurnBody = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "both done"}}},
		})
	}))
	defer server.Close()

	var askOrder []string
	ask := func(_ context.Context, req PermissionRequest) (bool, error) {
		askOrder = append(askOrder, req.ToolName)
		return true, nil
	}
	tk := New(WithPermission(Permission{Mode: PermissionAsk, Ask: ask})).
		Register(Calculator()).
		Register(DateTime())
	result, err := newAgent(t, tk, server.URL).Run(context.Background(), "compute and report time")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Both tool_calls in the single first turn must be executed, in order.
	tr := result.Steps[0].ToolResults
	if len(tr) != 2 {
		t.Fatalf("expected 2 tool results from one turn, got %d: %#v", len(tr), tr)
	}
	if tr[0].ToolName != "calculator" || tr[1].ToolName != "datetime" {
		t.Fatalf("expected calculator then datetime, got %q then %q", tr[0].ToolName, tr[1].ToolName)
	}
	if tr[0].ToolCallID != "call_calc" || tr[1].ToolCallID != "call_dt" {
		t.Fatalf("tool_call ids not preserved: %q, %q", tr[0].ToolCallID, tr[1].ToolCallID)
	}
	if tr[0].IsError || tr[1].IsError {
		t.Fatalf("expected both tools to succeed, got %#v", tr)
	}
	if !strings.Contains(tr[0].Content, "2") {
		t.Fatalf("expected calculator result 2, got %q", tr[0].Content)
	}
	if !strings.Contains(tr[1].Content, "Asia/Shanghai") {
		t.Fatalf("expected datetime to resolve timezone, got %q", tr[1].Content)
	}
	// The Ask gate must be consulted once per call, in the same order.
	if len(askOrder) != 2 || askOrder[0] != "calculator" || askOrder[1] != "datetime" {
		t.Fatalf("expected ask gate consulted per call in order, got %#v", askOrder)
	}
	// Both observations must be fed back before the model's final answer.
	if !strings.Contains(secondTurnBody, "call_calc") || !strings.Contains(secondTurnBody, "call_dt") {
		t.Fatalf("expected both tool results fed back into second turn, got: %s", secondTurnBody)
	}
	if result.FinalText != "both done" {
		t.Fatalf("expected final text after both tools, got %q", result.FinalText)
	}
}
