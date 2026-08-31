package joytoken

import (
	"encoding/json"
	"testing"
)

// A trimmed but structurally faithful non-streaming orchestration completion:
// metadata is a per-sub-task array, plus a top-level plan.
const orchestrationCompletionJSON = `{
  "id": "cmpl-orch-1",
  "object": "chat.completion",
  "model": "auto",
  "choices": [
    {"index": 0, "message": {"role": "assistant", "content": "final answer"}, "finish_reason": "stop"}
  ],
  "plan": [
    {"seq": 1, "task_id": "t1", "title": "search"},
    {"seq": 2, "task_id": "t2", "title": "write"}
  ],
  "metadata": [
    {"task_id": "__planner__", "task_seq": 0, "task_status": "done", "model": "planner-x", "tier": "economy", "tag": ["orchestration"], "latency": {"routing_ms": 12}, "billing": {"input_tokens": 10, "output_tokens": 5}},
    {"task_id": "t1", "task_seq": 1, "task_status": "done", "model": "gemini", "tier": "standard", "tag": ["search"], "billing": {"input_tokens": 20, "output_tokens": 8}},
    {"task_id": "__final__", "task_seq": 99, "task_status": "done", "model": "luna", "tier": "standard", "tag": ["aggregation"], "request_id": "req-final-777", "billing": {"input_tokens": 3, "output_tokens": 30}}
  ]
}`

func TestChatCompletionResponseDecodesOrchestrationMetadataArray(t *testing.T) {
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(orchestrationCompletionJSON), &resp); err != nil {
		t.Fatalf("unmarshal orchestration completion: %v", err)
	}

	if !resp.IsOrchestrated() {
		t.Fatalf("expected IsOrchestrated true")
	}
	if got := len(resp.MetadataList); got != 3 {
		t.Fatalf("MetadataList len = %d, want 3", got)
	}
	if got := len(resp.Plan); got != 2 {
		t.Fatalf("Plan len = %d, want 2", got)
	}
	if resp.Plan[0].Seq != 1 || resp.Plan[0].TaskID != "t1" || resp.Plan[0].Title != "search" {
		t.Fatalf("unexpected plan[0]: %+v", resp.Plan[0])
	}

	// Primary Metadata must resolve to the __final__ entry so RequestID works.
	if id := resp.RequestID(); id != "req-final-777" {
		t.Fatalf("RequestID = %q, want req-final-777", id)
	}

	// A modeled entry retains its typed fields and Extra keeps unknown keys.
	final := resp.MetadataList[2]
	if final.TaskID != OrchestrationFinalTaskID || len(final.Tag) != 1 || final.Tag[0] != "aggregation" || final.Billing == nil {
		t.Fatalf("unexpected final meta: %+v", final)
	}
	if final.Billing.OutputTokens != 30 {
		t.Fatalf("final output tokens = %d, want 30", final.Billing.OutputTokens)
	}
}

func TestNormalizeChatResponseSumsOrchestrationBilling(t *testing.T) {
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(orchestrationCompletionJSON), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	normalizeChatResponse(&resp, "")
	if resp.Usage == nil {
		t.Fatalf("expected aggregated usage from billing array")
	}
	// 10+20+3 input, 5+8+30 output.
	if resp.Usage.PromptTokens != 33 || resp.Usage.CompletionTokens != 43 {
		t.Fatalf("aggregated usage = %+v, want prompt=33 completion=43", resp.Usage)
	}
	if resp.Usage.TotalTokens != 76 {
		t.Fatalf("total = %d, want 76", resp.Usage.TotalTokens)
	}
}

func TestChatCompletionResponseDecodesPlainMetadataObject(t *testing.T) {
	// A plain (non-orchestrated) response must decode exactly as before: a
	// single metadata object, no MetadataList, RequestID readable.
	const plain = `{
      "id": "cmpl-plain",
      "choices": [{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
      "metadata": {"request_id": "req-plain-1", "billing": {"input_tokens": 4, "output_tokens": 6}}
    }`
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(plain), &resp); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if resp.IsOrchestrated() {
		t.Fatalf("plain response must not be orchestrated")
	}
	if resp.MetadataList != nil {
		t.Fatalf("plain response must have nil MetadataList")
	}
	if resp.RequestID() != "req-plain-1" {
		t.Fatalf("RequestID = %q, want req-plain-1", resp.RequestID())
	}
	normalizeChatResponse(&resp, "")
	if resp.Usage == nil || resp.Usage.PromptTokens != 4 || resp.Usage.CompletionTokens != 6 {
		t.Fatalf("plain usage = %+v, want prompt=4 completion=6", resp.Usage)
	}
}

func TestChatCompletionResponseNoMetadataStillDecodes(t *testing.T) {
	const noMeta = `{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(noMeta), &resp); err != nil {
		t.Fatalf("unmarshal no-metadata: %v", err)
	}
	if resp.Metadata != nil || resp.MetadataList != nil {
		t.Fatalf("expected nil metadata, got %v / %v", resp.Metadata, resp.MetadataList)
	}
	if resp.RequestID() != "" {
		t.Fatalf("RequestID should be empty")
	}
}

func TestChatCompletionChunkDecodesPlanningOrchestration(t *testing.T) {
	const planning = `{
      "id": "chunk-1",
      "object": "chat.completion.chunk",
      "choices": [],
      "orchestration": {
        "phase": "planning",
        "plan": [
          {"seq": 1, "task_id": "t1", "title": "search"},
          {"seq": 2, "task_id": "t2", "title": "write"}
        ]
      }
    }`
	var chunk ChatCompletionChunk
	if err := json.Unmarshal([]byte(planning), &chunk); err != nil {
		t.Fatalf("unmarshal planning chunk: %v", err)
	}
	if chunk.Orchestration == nil {
		t.Fatalf("expected orchestration on chunk")
	}
	if !chunk.Orchestration.IsPlanning() {
		t.Fatalf("expected IsPlanning true")
	}
	if got := len(chunk.Orchestration.Plan); got != 2 {
		t.Fatalf("plan len = %d, want 2", got)
	}
}

func TestChatCompletionChunkDecodesExecutingOrchestration(t *testing.T) {
	const executing = `{
      "id": "chunk-2",
      "object": "chat.completion.chunk",
      "choices": [{"index":0,"delta":{"content":"partial"}}],
      "orchestration": {"task_id": "t1", "task_seq": 1, "task_status": "running", "title": "search"}
    }`
	var chunk ChatCompletionChunk
	if err := json.Unmarshal([]byte(executing), &chunk); err != nil {
		t.Fatalf("unmarshal executing chunk: %v", err)
	}
	o := chunk.Orchestration
	if o == nil {
		t.Fatalf("expected orchestration on chunk")
	}
	if o.IsPlanning() {
		t.Fatalf("executing event must not be planning")
	}
	if o.TaskID != "t1" || o.TaskSeq != 1 || o.TaskStatus != "running" || o.Title != "search" {
		t.Fatalf("unexpected executing orchestration: %+v", o)
	}
	if len(chunk.Choices) != 1 || deltaText(chunk.Choices[0].Delta) != "partial" {
		t.Fatalf("expected delta text 'partial'")
	}
}

// realGatewayMetadataJSON mirrors an actual api-dev.joytokens.ai completion:
// metadata is an array even for a single non-orchestrated turn, tag is an
// array of strings, latency is a nested object, and billing carries a string
// credits_used plus cached counters. This exercises the true wire shape.
const realGatewayMetadataJSON = `{
  "id": "chatcmpl-ZjiVatWsOOnf1e8P3cGE0AM",
  "object": "chat.completion",
  "model": "gemini-3.5-flash",
  "created": 1788164201,
  "choices": [
    {"index": 0, "message": {"role": "assistant", "content": "Needs Assessment"}, "finish_reason": "length"}
  ],
  "metadata": [
    {
      "billing": {"cached_input_tokens": 0, "cached_output_tokens": 0, "credits_used": "0.033962", "input_tokens": 113, "output_tokens": 7},
      "billing_id": "bill-945fc124289bf007c3c0056d476b938c",
      "latency": {"routing_ms": 13},
      "model": "Gemini-3.5-Flash",
      "request_id": "auto-req-6255189a8109f404f436b466",
      "score": 0,
      "tag": ["reasoning"],
      "tier": "premium"
    }
  ]
}`

func TestChatCompletionResponseDecodesRealGatewayMetadata(t *testing.T) {
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(realGatewayMetadataJSON), &resp); err != nil {
		t.Fatalf("unmarshal real gateway metadata: %v", err)
	}
	if len(resp.MetadataList) != 1 {
		t.Fatalf("MetadataList len = %d, want 1 (array must parse)", len(resp.MetadataList))
	}
	m := resp.MetadataList[0]
	if m.Model != "Gemini-3.5-Flash" || m.Tier != "premium" {
		t.Fatalf("modeled fields lost: %+v", m)
	}
	if len(m.Tag) != 1 || m.Tag[0] != "reasoning" {
		t.Fatalf("tag = %v, want [reasoning]", m.Tag)
	}
	if m.Latency == nil || m.Latency.RoutingMS != 13 {
		t.Fatalf("latency = %+v, want routing_ms=13", m.Latency)
	}
	if m.Billing == nil || m.Billing.InputTokens != 113 || m.Billing.OutputTokens != 7 {
		t.Fatalf("billing = %+v, want in=113 out=7", m.Billing)
	}
	if resp.RequestID() != "auto-req-6255189a8109f404f436b466" {
		t.Fatalf("RequestID = %q", resp.RequestID())
	}
	// Unmodeled keys (billing_id, score) remain reachable via Extra.
	if _, ok := m.Extra["billing_id"]; !ok {
		t.Fatalf("expected billing_id retained in Extra")
	}
	normalizeChatResponse(&resp, "")
	if resp.Usage == nil || resp.Usage.PromptTokens != 113 || resp.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v, want prompt=113 completion=7", resp.Usage)
	}
}

func TestChatCompletionChunkDecodesArrayMetadata(t *testing.T) {
	const arrMeta = `{
      "id": "chunk-3",
      "choices": [],
      "metadata": [
        {"task_id": "t1", "request_id": "req-a"},
        {"task_id": "__final__", "request_id": "req-final"}
      ]
    }`
	var chunk ChatCompletionChunk
	if err := json.Unmarshal([]byte(arrMeta), &chunk); err != nil {
		t.Fatalf("unmarshal array-metadata chunk: %v", err)
	}
	if len(chunk.MetadataList) != 2 {
		t.Fatalf("MetadataList len = %d, want 2", len(chunk.MetadataList))
	}
	if chunk.RequestID() != "req-final" {
		t.Fatalf("RequestID = %q, want req-final", chunk.RequestID())
	}
}