// Command permission-demo is a minimal, real-SDK (NOT curl) demonstration of
// when the toolkit permission gate fires — and when it does not.
//
// It hits a real JoyToken gateway and runs the SAME prompt two ways so you can
// see the difference with your own eyes:
//
//	Demo A  RunChatCompletion + a tool wired under PermissionAsk
//	        -> the model asks for the tool, the loop executes it locally,
//	           and BEFORE execution the Ask callback fires:
//	               [permission] ASK tool=file_write ... -> ALLOW
//
//	Demo B  CreateChatCompletion where the tool is declared as SCHEMA ONLY
//	        (no local executable handler on the client)
//	        -> the gateway returns tool_calls but the SDK has nothing to
//	           execute, so it returns them unchanged and the permission gate
//	           is never reached. No ASK line. This is the bare-curl equivalent.
//
// The takeaway: the permission gate lives in the toolkit middleware around a
// tool's Execute. It only fires when (1) a tool is registered with an Execute
// through toolkit.New(WithPermission(Ask)) AND (2) the model's tool_call is
// actually EXECUTED locally. A tool_call for a schema-only (handler-less) tool
// is returned to the caller, never executed, so no permission check runs.
//
// Run it against a real gateway:
//
//	export JOY_TOKEN_API_KEY=your-key
//	export JOY_TOKEN_API_BASE_URL=https://api.joytokens.ai
//	go run ./permission-demo
//
// Optional: pick a tier that is not being throttled by an intermittent
// upstream circuit breaker, e.g. JOY_TOKEN_TIER=premium.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/agent/toolkit"
	"github.com/jd-opensource/joytoken-sdk-go/tooldef"
)

func main() {
	apiKey := os.Getenv("JOY_TOKEN_API_KEY")
	if apiKey == "" {
		log.Fatal("Set JOY_TOKEN_API_KEY before running the permission demo.")
	}
	baseURL := os.Getenv("JOY_TOKEN_API_BASE_URL")
	tier := os.Getenv("JOY_TOKEN_TIER") // "" = gateway default

	ctx := context.Background()

	// Language for the permission prompt. The SDK is language-agnostic; the
	// host picks the wording. JOY_TOKEN_LANG=zh|en overrides the system locale.
	lang := detectLang()

	sandboxRoot, err := os.MkdirTemp("", "joytoken-permission-*")
	if err != nil {
		log.Fatalf("create sandbox: %v", err)
	}

	// A single tool, file_write, wired under PermissionAsk. The Ask callback is
	// the ONLY place a permission decision is made — and it only runs when the
	// tool is actually executed.
	askFired := false
	writeKit := toolkit.New(toolkit.WithPermission(toolkit.Permission{
		Mode: toolkit.PermissionAsk,
		Ask: func(_ context.Context, req toolkit.PermissionRequest) (bool, error) {
			askFired = true
			// The prompt text shown to the user is 100% the host's choice — the
			// SDK only hands us the structured PermissionRequest. Here we render
			// it in the language selected by JOY_TOKEN_LANG / the system locale.
			return promptPermission(lang, sandboxRoot, req), nil
		},
	})).Register(toolkit.FileWrite(toolkit.FileSandbox{Root: sandboxRoot}))

	tools := writeKit.Tools()

	// Demo A client: has the executable tool registered (WithTools), so the
	// RunChatCompletion loop will run it locally and hit the permission gate.
	client := newClient(apiKey, baseURL, tools)

	// Demo B client: NO local tools registered. We only send the file_write
	// SCHEMA in the request so the model still emits a tool_call, but the SDK
	// has no executable handler for it -> it returns the tool_calls unchanged
	// and never executes anything. This is the "bare curl" equivalent.
	schemaOnly := newClient(apiKey, baseURL, nil)
	writeSchema := tooldef.ToChatTool(tools[0])

	prompt := "Call the file_write tool to write a file named note.txt " +
		"with the content \"hello-from-sdk\". Use the tool, do not answer from memory."

	fmt.Println("=========================================================")
	fmt.Println("JoyToken permission demo (real SDK, not curl)")
	fmt.Printf("base URL: %s\n", displayBaseURL(baseURL))
	fmt.Printf("tier:     %s\n", tierLabel(tier))
	fmt.Printf("lang:     %s (set JOY_TOKEN_LANG=zh|en to switch)\n", lang)
	fmt.Printf("sandbox:  %s\n", sandboxRoot)
	fmt.Println("=========================================================")

	// ---- Demo A: RunChatCompletion — the loop EXECUTES tools, gate fires. ----
	fmt.Println("\n########## Demo A: RunChatCompletion (executes tools) ##########")
	fmt.Println("Expect a [permission] ASK line before the tool runs.")
	askFired = false
	resultA, err := client.RunChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model:     joytoken.ModelAuto,
		Tier:      tier,
		Messages:  []joytoken.ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens: intPtr(300),
	}, joytoken.RunChatOptions{MaxSteps: 6})
	if err != nil {
		fmt.Printf("Demo A failed (likely intermittent upstream): %v\n", err)
	} else {
		fmt.Printf("steps=%d  stopped_by=%s  finish=%s\n", len(resultA.Steps), resultA.StoppedBy, resultA.FinishReason)
		fmt.Printf("final: %s\n", truncate(resultA.FinalText, 200))
		fmt.Printf(">>> permission ASK fired in Demo A? %v\n", askFired)
	}

	// Give the per-second rate-limit window a moment before Demo B.
	time.Sleep(3 * time.Second)

	// ---- Demo B: CreateChatCompletion — single-shot, does NOT execute. ----
	fmt.Println("\n########## Demo B: CreateChatCompletion (single-shot) ##########")
	fmt.Println("Expect NO permission line: tool_calls are returned but never executed.")
	askFired = false
	respB, err := schemaOnly.CreateChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model:     joytoken.ModelAuto,
		Tier:      tier,
		Messages:  []joytoken.ChatMessage{{Role: "user", Content: prompt}},
		Tools:     []joytoken.ChatTool{writeSchema}, // schema only, no executable handler
		MaxTokens: intPtr(300),
	})
	if err != nil {
		fmt.Printf("Demo B failed (likely intermittent upstream): %v\n", err)
	} else if len(respB.Choices) > 0 {
		msg := respB.Choices[0].Message
		fmt.Printf("finish=%s  tool_calls=%d\n", respB.Choices[0].FinishReason, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			fmt.Printf("  model requested tool=%s args=%s (NOT executed by SDK)\n",
				tc.Function.Name, truncate(tc.Function.Arguments, 120))
		}
		fmt.Printf(">>> permission ASK fired in Demo B? %v  <-- stays false: single-shot never executes tools\n", askFired)
	}

	fmt.Println("\n=========================================================")
	fmt.Println("Conclusion:")
	fmt.Println("  - Permission (ASK) is a toolkit middleware around a tool's Execute.")
	fmt.Println("  - It fires ONLY when the tool_call is actually executed locally,")
	fmt.Println("    i.e. the tool was registered WITH an Execute handler (Demo A).")
	fmt.Println("  - A schema-only tool_call (Demo B, like a bare curl) is returned")
	fmt.Println("    to the caller and never executed, so the gate is never reached.")
	fmt.Println("=========================================================")
}

func newClient(apiKey, baseURL string, tools []joytoken.Tool) *joytoken.Client {
	opts := []joytoken.Option{
		joytoken.WithAPIKey(apiKey),
		joytoken.WithTools(tools...),
		joytoken.WithTimeout(90 * time.Second),
	}
	if baseURL != "" {
		opts = append(opts, joytoken.WithAPIBaseURL(baseURL))
	}
	return joytoken.NewClient(opts...)
}

func intPtr(v int) *int { return &v }

func tierLabel(t string) string {
	if t == "" {
		return "(gateway default)"
	}
	return t
}

func displayBaseURL(b string) string {
	if b == "" {
		return "(SDK default)"
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// detectLang picks the permission-prompt language. JOY_TOKEN_LANG takes
// precedence (values "zh" or "en"); otherwise it falls back to the system
// LANG/LC_ALL locale, defaulting to English.
func detectLang() string {
	if v := strings.ToLower(os.Getenv("JOY_TOKEN_LANG")); v != "" {
		if strings.HasPrefix(v, "zh") {
			return "zh"
		}
		return "en"
	}
	locale := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LANG"))
	if strings.Contains(locale, "zh") {
		return "zh"
	}
	return "en"
}

// promptPermission renders a LOCALIZED approval prompt for a pending tool call
// and reads y/n from stdin. This is entirely host-side: the SDK never produces
// user-facing text, it only hands us the structured PermissionRequest, so the
// wording and language are ours to choose.
func promptPermission(lang, sandboxRoot string, req toolkit.PermissionRequest) bool {
	// Render a clear, Codex/Claude-style action card so the user sees exactly
	// what the tool wants to touch — not a raw map dump.
	fmt.Println(describeToolCall(lang, sandboxRoot, req))

	var question, hint, allowed, denied string
	switch lang {
	case "zh":
		question = "  是否允许此操作?(y/n): "
		hint = "  未读到输入,默认拒绝。\n"
		allowed = "  ✓ 已允许\n"
		denied = "  ✗ 已拒绝\n"
	default:
		question = "  Allow this action? (y/n): "
		hint = "  No input read; denying by default.\n"
		allowed = "  ✓ Allowed\n"
		denied = "  ✗ Denied\n"
	}

	fmt.Print(question)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		fmt.Print(hint)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		fmt.Print(allowed)
		return true
	}
	fmt.Print(denied)
	return false
}

// describeToolCall turns the structured PermissionRequest into an explicit,
// human-readable action card. It special-cases file_write to show the resolved
// ABSOLUTE target path, byte size, and a content preview — the same clarity
// Codex/Claude give before a write. Unknown tools fall back to a key/value dump.
func describeToolCall(lang, sandboxRoot string, req toolkit.PermissionRequest) string {
	args, _ := req.Input.(map[string]any)

	var b strings.Builder
	zh := lang == "zh"
	if zh {
		fmt.Fprintf(&b, "\n  ┌─ 权限请求 (步骤 %d)\n", req.Step)
	} else {
		fmt.Fprintf(&b, "\n  ┌─ Permission request (step %d)\n", req.Step)
	}

	switch req.ToolName {
	case "file_write":
		rel, _ := args["path"].(string)
		content, _ := args["content"].(string)
		abs := rel
		if sandboxRoot != "" && rel != "" {
			abs = filepath.Join(sandboxRoot, rel)
		}
		if zh {
			fmt.Fprintf(&b, "  │ 操作:   写入文件 (file_write)\n")
			fmt.Fprintf(&b, "  │ 目标:   %s\n", abs)
			fmt.Fprintf(&b, "  │ 相对:   %s\n", rel)
			fmt.Fprintf(&b, "  │ 大小:   %d 字节\n", len(content))
			fmt.Fprintf(&b, "  │ 内容预览:\n")
		} else {
			fmt.Fprintf(&b, "  │ Action:  write file (file_write)\n")
			fmt.Fprintf(&b, "  │ Target:  %s\n", abs)
			fmt.Fprintf(&b, "  │ Rel:     %s\n", rel)
			fmt.Fprintf(&b, "  │ Size:    %d bytes\n", len(content))
			fmt.Fprintf(&b, "  │ Preview:\n")
		}
		for _, ln := range strings.Split(truncate(content, 400), "\n") {
			fmt.Fprintf(&b, "  │   %s\n", ln)
		}
	default:
		if zh {
			fmt.Fprintf(&b, "  │ 工具:   %s\n  │ 入参:   %v\n", req.ToolName, req.Input)
		} else {
			fmt.Fprintf(&b, "  │ Tool:   %s\n  │ Input:  %v\n", req.ToolName, req.Input)
		}
	}
	b.WriteString("  └─")
	return b.String()
}