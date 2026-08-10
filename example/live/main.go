package main

import (
	"context"
	"fmt"
	"log"
	"os"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

func main() {
	apiKey := os.Getenv("JOY_TOKEN_API_KEY")
	if apiKey == "" {
		log.Fatal("Set JOY_TOKEN_API_KEY before running the live JoyToken Go example.")
	}

	model := getenv("JOY_TOKEN_MODEL", "auto")

	client := joytoken.NewClient(
		joytoken.WithAPIKey(apiKey),
	)

	ctx := context.Background()

	fmt.Println("JoyToken live Go example")
	fmt.Printf("Model: %s\n", model)

	models, err := client.ListModels(ctx)
	if err != nil {
		log.Fatalf("list models: %v", err)
	}
	fmt.Printf("Models returned: %d\n", len(models.Data))
	if len(models.Data) > 0 {
		fmt.Printf("First model: %s\n", models.Data[0].ID)
	}

	completion, err := client.CreateChatCompletion(ctx, joytoken.ChatCompletionRequest{
		Model: model,
		Messages: []joytoken.ChatMessage{
			{Role: "user", Content: "Reply with one short sentence confirming JoyToken is connected from Go."},
		},
		MaxTokens: intPtr(80),
	})
	if err != nil {
		log.Fatalf("create chat completion: %v", err)
	}
	if len(completion.Choices) == 0 {
		log.Fatal("create chat completion returned no choices")
	}

	fmt.Println("\nGo Client SDK response:")
	fmt.Println(completion.Choices[0].Message.Content)

	runner := agent.New(agent.AgentOptions{
		ModelName: model,
		Model:     agent.NewJoyTokenProvider(client, agent.WithDefaultModel(model)),
		System:    "You are testing the JoyToken Agent SDK. Keep the answer short.",
		MaxTokens: intPtr(80),
	})
	result, err := runner.Run(ctx, "Confirm the Agent SDK can call JoyToken through the configured provider.")
	if err != nil {
		log.Fatalf("run agent: %v", err)
	}

	fmt.Println("\nGo Agent SDK response:")
	fmt.Println(result.FinalText)
	fmt.Printf("Usage total tokens: %d\n", result.Usage.TotalTokens)
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intPtr(value int) *int {
	return &value
}
