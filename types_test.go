package joytoken

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseFunctionToolSerialization(t *testing.T) {
	tool := ResponseTool{Type: "function", Name: "get_weather", Description: "Get the weather", Parameters: map[string]any{"type": "object"}}
	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"get_weather"`) || strings.Contains(string(data), "vector_store_ids") {
		t.Fatalf("unexpected function tool JSON: %s", data)
	}
}

func TestHostedResponseToolSerializationWithoutFunctionEnvelope(t *testing.T) {
	tool := ResponseTool{Type: "web_search_preview", SearchContextSize: "medium", UserLocation: &ResponseToolUserLocation{Type: "approximate", Country: "CN"}}
	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	jsonText := string(data)
	for _, want := range []string{`"type":"web_search_preview"`, `"search_context_size":"medium"`, `"country":"CN"`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("expected %s in %s", want, jsonText)
		}
	}
	if strings.Contains(jsonText, `"function"`) {
		t.Fatalf("hosted tool must not carry function envelope: %s", jsonText)
	}
}

func TestResponseFileSearchSerialization(t *testing.T) {
	maxResults := 20
	threshold := 0.5
	tool := ResponseTool{
		Type: "file_search", VectorStoreIDs: []string{"vs_123"}, MaxNumResults: &maxResults,
		RankingOptions: &ResponseToolRankingOptions{Ranker: "auto", ScoreThreshold: &threshold},
	}
	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"vector_store_ids":["vs_123"]`, `"max_num_results":20`, `"score_threshold":0.5`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected %s in %s", want, data)
		}
	}
}
