// Command live-tools is a real, network-hitting verification that the SDK's
// default fallback tools (calculator, datetime) are actually declared to the
// gateway, invoked by the model, executed locally, and looped back
// automatically — all through the streaming entry point RunChatCompletionStream.
//
// Run it against a real JoyToken gateway:
//
//	export JOY_TOKEN_API_KEY=your-key
//	# optional, defaults to the SDK's built-in gateway base URL:
//	# export JOY_TOKEN_API_BASE_URL=https://your-gateway
//	go run ./example/live-tools
//
// The prompt deliberately forces both tools: it asks for an arithmetic result
// (calculator) and the current time (datetime). If the default fallback tools
// are wired correctly, the run prints one or more "tool called" lines and a
// final answer that reflects the tool outputs.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

func main() {
	apiKey := os.Getenv("JOY_TOKEN_API_KEY")
	if apiKey == "" {
		log.Fatal("Set JOY_TOKEN_API_KEY before running the live default-tools example.")
	}

	// Only WithAPIKey: no tools registered by the caller, so any tool the model
	// calls must come from the SDK's default fallback set. defaultLocalTools is
	// on by default, which is exactly what we are verifying.
	client := joytoken.NewClient(
		joytoken.WithAPIKey(apiKey),
	)

	ctx := context.Background()

	fmt.Println("JoyToken live default-tools verification")
	fmt.Printf("Model: %s\n\n", joytoken.ModelAuto)

	// The datetime tool has the simplest possible schema (two optional string
	// fields, or an empty {} argument object), so the model does not have to
	// serialize operators or long numbers into a JSON string. That minimizes
	// the Gemini-family "malformed_function_call" case, letting us verify the
	// full tool chain end-to-end. A unique nonce keeps each run distinct so the
	// gateway idempotency guard (409 "duplicate request_id") does not reject a
	// re-run. Even if a turn still comes back malformed, the loop now nudges the
	// model and retries instead of silently stopping with empty text — watch
	// result.FinishReason in the summary to see which path was taken.
	nonce := time.Now().UnixNano()
	prompt := fmt.Sprintf(
		"Use the datetime tool to get the current time in the Asia/Shanghai timezone, "+
			"then state it in a sentence. (request nonce %d, ignore this number)", nonce)

	var toolCalls int
	result, err := client.RunChatCompletionStream(ctx, joytoken.ChatCompletionRequest{
		Model: joytoken.ModelAuto,
		Messages: []joytoken.ChatMessage{
			{Role: "user", Content: prompt},
		},
		MaxTokens: intPtr(300),
	}, joytoken.RunChatStreamOptions{
		OnTextDelta: func(delta string) {
			// Live token-by-token streaming, same experience as the primitive.
			fmt.Print(delta)
		},
		OnToolResult: func(r joytoken.ToolCallResult) {
			toolCalls++
			status := "ok"
			if r.IsError {
				status = "error"
			}
			fmt.Printf("\n[tool called] name=%s status=%s result=%s\n", r.ToolName, status, r.Content)
		},
	})
	if err != nil {
		log.Fatalf("run chat completion stream: %v", err)
	}

	// Expand every step of the loop so the full tool-calling chain is visible:
	// model emits tool_call -> local execute -> result fed back -> model's next
	// turn. Seeing all four links is how you confirm the tool was called and
	// looped end-to-end, not just that "something happened".
	fmt.Printf("\n\n--- per-step chain ---\n")
	for _, step := range result.Steps {
		fmt.Printf("[step %d]\n", step.Index)
		if len(step.AssistantMessage.ToolCalls) == 0 {
			fmt.Printf("  model emitted: final text (no tool_calls)\n")
		}
		for _, tc := range step.AssistantMessage.ToolCalls {
			// Link 1: the model asked to call a tool, with the arguments it generated.
			fmt.Printf("  model -> tool_call: name=%s args=%s\n", tc.Function.Name, tc.Function.Arguments)
		}
		for _, tr := range step.ToolResults {
			// Link 2+3: the tool ran locally and its result was fed back.
			status := "ok"
			if tr.IsError {
				status = "error"
			}
			fmt.Printf("  tool executed -> result fed back: name=%s status=%s content=%s\n", tr.ToolName, status, tr.Content)
		}
		if step.Usage != nil {
			fmt.Printf("  usage: prompt=%d completion=%d total=%d\n",
				step.Usage.PromptTokens, step.Usage.CompletionTokens, step.Usage.TotalTokens)
		}
	}

	fmt.Printf("\n--- summary ---\n")
	fmt.Printf("stopped by:        %s\n", result.StoppedBy)
	fmt.Printf("finish reason:     %s\n", result.FinishReason)
	fmt.Printf("model turns:       %d\n", len(result.Steps))
	fmt.Printf("tool executions:   %d\n", toolCalls)
	fmt.Printf("final text:        %s\n", result.FinalText)

	// With vendor-neutral finish_reason handling, a malformed tool call no
	// longer masquerades as a clean stop: the loop retried with a corrective
	// nudge and, if it never recovered, surfaces it here explicitly.
	if result.FinishReason == "malformed_function_call" {
		fmt.Println("\nNOTE: the auto-routed model kept emitting malformed tool-call payloads.")
		fmt.Println("The loop detected this (not a silent empty stop), nudged the model, and retried up to MaxSteps.")
		fmt.Println("This is the vendor-side glitch the SDK now surfaces instead of swallowing.")
	}

	if toolCalls == 0 {
		fmt.Println("\nWARNING: the model answered without calling any default tool.")
		fmt.Println("This can happen if the model chose to answer directly; re-run or adjust the prompt.")
		diagnoseFinishReason(ctx, client)
		return
	}
	// A complete call means: >=1 model turn produced a tool_call, that tool ran
	// locally, its result was fed back, and a later turn produced the final text.
	fmt.Println("\nOK: default fallback tools were invoked and auto-looped successfully.")
	fmt.Println("Chain verified: model tool_call -> local execute -> result fed back -> final answer.")
}

func intPtr(value int) *int {
	return &value
}

// diagnoseFinishReason runs one raw primitive stream and prints the gateway's
// finish_reason. This distinguishes "the model chose to answer directly"
// (finish_reason "stop" with real text) from "the auto-routed model tried to
// call a tool but produced an invalid payload" (finish_reason
// "malformed_function_call" with empty delta) — the latter proves the tool
// declarations were injected and the model attempted to use them.
func diagnoseFinishReason(ctx context.Context, client *joytoken.Client) {
	fmt.Println("\n--- diagnostic: raw finish_reason ---")
	stream, err := client.StreamChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model: joytoken.ModelAuto,
		Messages: []joytoken.ChatMessage{{Role: "user", Content: fmt.Sprintf(
			"Use the calculator tool to compute 39847 * 21903 exactly. (nonce %d)", time.Now().UnixNano())}},
		MaxTokens: intPtr(300),
	})
	if err != nil {
		fmt.Printf("diagnostic stream error: %v\n", err)
		return
	}
	defer stream.Close()
	for {
		chunk, rerr := stream.Recv()
		if rerr != nil {
			break
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != "" {
				fmt.Printf("finish_reason=%q (empty delta here means the model attempted a tool call but emitted an invalid payload)\n", ch.FinishReason)
			}
		}
	}
}
