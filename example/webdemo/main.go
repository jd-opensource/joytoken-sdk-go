// Command webdemo is a minimal web demo that visualizes the
// "default-on Tools + graceful degradation" design of the JoyToken SDK.
//
// A single HTML page exposes an "Enable Tools" switch. When the switch is ON,
// the backend routes the request through the agent SDK with default tools
// (toolkit.NewAgent). When it is OFF, the backend uses the bare client for a
// plain, single-shot chat. This mirrors the recommended usage described in
// docs/agent-toolkit-architecture.md section 8.
//
// Run without an API key to explore the UI in mock mode:
//
//	go run ./example/webdemo
//
// Then open http://localhost:8080 in a browser.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/agent"
	"github.com/jd-opensource/joytoken-sdk-go/agent/toolkit"
)

type chatRequest struct {
	Message     string `json:"message"`
	EnableTools bool   `json:"enableTools"`
}

type chatResponse struct {
	Reply string `json:"reply"`
	Path  string `json:"path"`  // "agent+tools" or "client-only" or "mock"
	Tools bool   `json:"tools"` // whether tools were enabled for this call
}

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	apiKey := os.Getenv("JOY_TOKEN_API_KEY")

	var client *joytoken.Client
	var plainClient *joytoken.Client
	if apiKey != "" {
		client = joytoken.NewClient(joytoken.WithAPIKey(apiKey))
		// plainClient disables the default local tools so the "switch OFF"
		// branch is a genuine single-shot chat: with no tools injected the
		// model has nothing locally executable, so CreateChatCompletion never
		// enters its auto tool-execution loop.
		plainClient = joytoken.NewClient(
			joytoken.WithAPIKey(apiKey),
			joytoken.WithDefaultLocalTools(false),
		)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		handleChat(w, r, client, plainClient)
	})

	log.Printf("JoyToken webdemo listening on http://localhost%s", addr)
	if apiKey == "" {
		log.Printf("JOY_TOKEN_API_KEY not set -> running in MOCK mode (no real model calls)")
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleChat(w http.ResponseWriter, r *http.Request, client *joytoken.Client, plainClient *joytoken.Client) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var resp chatResponse

	switch {
	case client == nil:
		// Mock mode: no API key. Show the branch that WOULD run.
		resp = mockReply(req)
	case req.EnableTools:
		// Switch ON -> agent SDK with default tools, auto multi-step execution.
		resp = runWithTools(ctx, client, req.Message)
	default:
		// Switch OFF -> bare client with default tools disabled, plain
		// single-shot chat that does not enter the auto tool loop.
		resp = runClientOnly(ctx, plainClient, req.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// runWithTools uses the agent SDK with default tools injected.
func runWithTools(ctx context.Context, client *joytoken.Client, message string) chatResponse {
	runner := toolkit.NewAgent(agent.AgentOptions{
		Model:     agent.NewJoyTokenProvider(client),
		System:    "You are a helpful assistant with access to local tools.",
		MaxTokens: agent.Int(200),
	})
	result, err := runner.Run(ctx, message)
	if err != nil {
		return chatResponse{Reply: "error: " + err.Error(), Path: "agent+tools", Tools: true}
	}
	return chatResponse{Reply: result.FinalText, Path: "agent+tools", Tools: true}
}

// runClientOnly uses a client with default local tools disabled for a genuine
// single-shot chat. Because no tools are injected, the model cannot return a
// locally executable tool_call, so CreateChatCompletion returns the first
// response without running its auto tool-execution loop.
func runClientOnly(ctx context.Context, client *joytoken.Client, message string) chatResponse {
	completion, err := client.CreateChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model: joytoken.ModelAuto,
		Messages: []joytoken.ChatMessage{
			{Role: "user", Content: message},
		},
		MaxTokens: agent.Int(200),
	})
	if err != nil {
		return chatResponse{Reply: "error: " + err.Error(), Path: "client-only", Tools: false}
	}
	if len(completion.Choices) == 0 {
		return chatResponse{Reply: "(no reply)", Path: "client-only", Tools: false}
	}
	return chatResponse{Reply: contentString(completion.Choices[0].Message.Content), Path: "client-only", Tools: false}
}

// contentString normalizes a ChatMessage.Content (typed as any) into a string.
func contentString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case nil:
		return "(no reply)"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func mockReply(req chatRequest) chatResponse {
	if req.EnableTools {
		return chatResponse{
			Reply: fmt.Sprintf("[MOCK · agent+tools] 你问:%q。开关=开,请求会走 agent SDK(toolkit.NewAgent),自动注入 calculator/datetime 等默认工具并多轮执行。设置 JOY_TOKEN_API_KEY 后即为真实回复。", req.Message),
			Path:  "mock",
			Tools: true,
		}
	}
	return chatResponse{
		Reply: fmt.Sprintf("[MOCK · client-only] 你问:%q。开关=关,请求会走裸 joytoken.Client,单轮纯对话、不执行工具。设置 JOY_TOKEN_API_KEY 后即为真实回复。", req.Message),
		Path:  "mock",
		Tools: false,
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}
