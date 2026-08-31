package joytoken

import "encoding/json"

// Well-known orchestration task identifiers surfaced by the Gateway. The
// planner entry carries the plan (seq 0); the final entry carries the
// aggregated answer.
const (
	// OrchestrationPlannerTaskID is the task id of the planning step.
	OrchestrationPlannerTaskID = "__planner__"
	// OrchestrationFinalTaskID is the task id of the aggregated final answer.
	OrchestrationFinalTaskID = "__final__"
	// OrchestrationPhasePlanning is the orchestration phase value emitted while
	// the plan is being produced.
	OrchestrationPhasePlanning = "planning"
)

// PlanEntry is one planned sub-task announced by the orchestrator.
type PlanEntry struct {
	Seq    int    `json:"seq"`
	TaskID string `json:"task_id"`
	Title  string `json:"title,omitempty"`
}

// OrchestrationBilling holds the per-sub-task token accounting reported in
// orchestration metadata. The Gateway reports credits_used as a decimal string.
type OrchestrationBilling struct {
	InputTokens        int    `json:"input_tokens,omitempty"`
	OutputTokens       int    `json:"output_tokens,omitempty"`
	CachedInputTokens  int    `json:"cached_input_tokens,omitempty"`
	CachedOutputTokens int    `json:"cached_output_tokens,omitempty"`
	CreditsUsed        string `json:"credits_used,omitempty"`
}

// OrchestrationLatency holds the nested latency breakdown the Gateway reports
// per metadata entry.
type OrchestrationLatency struct {
	RoutingMS int `json:"routing_ms,omitempty"`
}

// OrchestrationTaskMeta is one entry of the per-sub-task metadata array a
// completion carries. The Gateway sends metadata as an array even for a single
// turn, so this doubles as the shape of any real metadata entry. task_id/seq/
// status are only present when multi-step orchestration is engaged. Any field
// not modeled here (billing_id, score, model_recommendation, task_score, ...)
// is retained in Extra.
type OrchestrationTaskMeta struct {
	RequestID  string                `json:"request_id,omitempty"`
	Model      string                `json:"model,omitempty"`
	Tier       string                `json:"tier,omitempty"`
	Tag        []string              `json:"tag,omitempty"`
	TaskID     string                `json:"task_id,omitempty"`
	TaskSeq    int                   `json:"task_seq,omitempty"`
	TaskStatus string                `json:"task_status,omitempty"`
	Latency    *OrchestrationLatency `json:"latency,omitempty"`
	Billing    *OrchestrationBilling `json:"billing,omitempty"`

	// Extra retains any fields not modeled above so callers keep access to the
	// raw metadata object without a second parse.
	Extra map[string]any `json:"-"`
}

// ChunkOrchestration is the top-level "orchestration" object on a streaming
// chunk. During planning it carries Phase and Plan; during execution it
// carries the currently running sub-task's identity and status.
type ChunkOrchestration struct {
	Phase      string      `json:"phase,omitempty"`
	Plan       []PlanEntry `json:"plan,omitempty"`
	TaskID     string      `json:"task_id,omitempty"`
	TaskSeq    int         `json:"task_seq,omitempty"`
	TaskStatus string      `json:"task_status,omitempty"`
	Title      string      `json:"title,omitempty"`
}

// IsPlanning reports whether this chunk orchestration event carries the plan.
func (o *ChunkOrchestration) IsPlanning() bool {
	return o != nil && (o.Phase == OrchestrationPhasePlanning || len(o.Plan) > 0)
}

// primaryMetadata returns the object used for the legacy Metadata field from a
// per-sub-task metadata array. It prefers the final aggregation entry, then the
// planner, then the first entry, so RequestID()/usage keep working sensibly.
func primaryMetadata(list []OrchestrationTaskMeta) map[string]any {
	if len(list) == 0 {
		return nil
	}
	pick := 0
	for i, m := range list {
		if m.TaskID == OrchestrationFinalTaskID {
			pick = i
			break
		}
	}
	return list[pick].asObject()
}

// asObject rebuilds a generic map from a task metadata entry so existing
// map-based helpers (RequestIDFromMetadata, metadataTokenUsage) keep working.
func (m OrchestrationTaskMeta) asObject() map[string]any {
	out := make(map[string]any, len(m.Extra)+8)
	for k, v := range m.Extra {
		out[k] = v
	}
	if m.RequestID != "" {
		out["request_id"] = m.RequestID
	}
	if m.Model != "" {
		out["model"] = m.Model
	}
	if m.Tier != "" {
		out["tier"] = m.Tier
	}
	if len(m.Tag) > 0 {
		tags := make([]any, len(m.Tag))
		for i, t := range m.Tag {
			tags[i] = t
		}
		out["tag"] = tags
	}
	if m.TaskID != "" {
		out["task_id"] = m.TaskID
	}
	if m.Billing != nil {
		if _, ok := out["billing"]; !ok {
			out["billing"] = map[string]any{
				"input_tokens":  m.Billing.InputTokens,
				"output_tokens": m.Billing.OutputTokens,
			}
		}
	}
	return out
}

// decodeMetadataField inspects a raw JSON "metadata" value and returns whichever
// shape it carries: a single object (obj) or a per-sub-task array (list). A
// null or absent field yields both nil. Malformed shapes are ignored so a
// stream stays resilient.
func decodeMetadataField(raw json.RawMessage) (obj map[string]any, list []OrchestrationTaskMeta) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := skipJSONSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		var entries []OrchestrationTaskMeta
		if err := unmarshalMetaArray(trimmed, &entries); err == nil {
			return primaryMetadata(entries), entries
		}
	case '{':
		var m map[string]any
		if err := json.Unmarshal(trimmed, &m); err == nil {
			return m, nil
		}
	}
	return nil, nil
}

// unmarshalMetaArray decodes the metadata array while also retaining unmodeled
// fields of each entry in Extra.
func unmarshalMetaArray(raw json.RawMessage, out *[]OrchestrationTaskMeta) error {
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(raw, &rawEntries); err != nil {
		return err
	}
	entries := make([]OrchestrationTaskMeta, 0, len(rawEntries))
	for _, re := range rawEntries {
		var meta OrchestrationTaskMeta
		if err := json.Unmarshal(re, &meta); err != nil {
			return err
		}
		var extra map[string]any
		if err := json.Unmarshal(re, &extra); err == nil {
			meta.Extra = extra
		}
		entries = append(entries, meta)
	}
	*out = entries
	return nil
}

func skipJSONSpace(raw json.RawMessage) json.RawMessage {
	i := 0
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return raw[i:]
		}
	}
	return raw[i:]
}

// UnmarshalJSON decodes a completion, tolerating a "metadata" field that is
// either a single object (plain response) or a per-sub-task array (Gateway
// orchestration). Non-orchestrated bytes decode identically to the default.
func (r *ChatCompletionResponse) UnmarshalJSON(data []byte) error {
	type alias ChatCompletionResponse
	aux := struct {
		Metadata json.RawMessage `json:"metadata,omitempty"`
		*alias
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	obj, list := decodeMetadataField(aux.Metadata)
	r.Metadata = obj
	r.MetadataList = list
	return nil
}

// UnmarshalJSON decodes a streaming chunk, tolerating the orchestration object
// and an array-shaped metadata field. Non-orchestrated bytes decode as before.
func (c *ChatCompletionChunk) UnmarshalJSON(data []byte) error {
	type alias ChatCompletionChunk
	aux := struct {
		Metadata json.RawMessage `json:"metadata,omitempty"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	obj, list := decodeMetadataField(aux.Metadata)
	c.Metadata = obj
	c.MetadataList = list
	return nil
}