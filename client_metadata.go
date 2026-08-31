package joytoken

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// RequestIDFromMetadata returns the Gateway request ID carried in a response
// metadata object. JoyToken includes this body-level fallback even when an
// upstream response does not provide a request ID header.
func RequestIDFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	requestID, _ := metadata["request_id"].(string)
	return requestID
}

func requestIDFromHeaders(headers http.Header) string {
	return firstHeader(headers, "X-DAOE-Request-ID", "X-Request-ID")
}

func requestIDFromBody(body any) string {
	object, _ := body.(map[string]any)
	if object == nil {
		return ""
	}
	if requestID, _ := object["request_id"].(string); requestID != "" {
		return requestID
	}
	if requestID := RequestIDFromMetadata(asObject(object["metadata"])); requestID != "" {
		return requestID
	}
	if nested := asObject(object["error"]); nested != nil {
		if requestID, _ := nested["request_id"].(string); requestID != "" {
			return requestID
		}
	}
	return ""
}

func asObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func metadataWithRequestID(metadata map[string]any, requestID string) map[string]any {
	if requestID == "" || RequestIDFromMetadata(metadata) != "" {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["request_id"] = requestID
	return metadata
}

// metadataTokenUsage reads the stable token counters from JoyToken billing
// metadata. It is used only when the provider omitted the protocol-level usage
// object; an explicit protocol usage object always remains authoritative.
func metadataTokenUsage(metadata map[string]any) (input, output int, ok bool) {
	billing, _ := metadata["billing"].(map[string]any)
	if billing == nil {
		return 0, 0, false
	}
	input, inputOK := metadataInt(billing["input_tokens"])
	output, outputOK := metadataInt(billing["output_tokens"])
	return input, output, inputOK || outputOK
}

func metadataInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int8:
		return int(number), true
	case int16:
		return int(number), true
	case int32:
		return int(number), true
	case int64:
		return int(number), true
	case uint:
		return int(number), true
	case uint8:
		return int(number), true
	case uint16:
		return int(number), true
	case uint32:
		return int(number), true
	case uint64:
		if number > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(number), true
	case float64:
		if number < 0 || number != float64(int(number)) {
			return 0, false
		}
		return int(number), true
	case float32:
		if number < 0 || number != float32(int(number)) {
			return 0, false
		}
		return int(number), true
	case json.Number:
		parsed, err := strconv.Atoi(number.String())
		return parsed, err == nil
	case string:
		parsed, err := strconv.Atoi(number)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// metadataListTokenUsage sums the billing counters across a per-sub-task
// orchestration metadata array. ok is true when at least one entry reported a
// token count.
func metadataListTokenUsage(list []OrchestrationTaskMeta) (input, output int, ok bool) {
	for _, m := range list {
		if m.Billing == nil {
			continue
		}
		input += m.Billing.InputTokens
		output += m.Billing.OutputTokens
		if m.Billing.InputTokens != 0 || m.Billing.OutputTokens != 0 {
			ok = true
		}
	}
	return input, output, ok
}

func normalizeChatUsage(usage **Usage, metadata map[string]any) {
	if *usage == nil {
		input, output, ok := metadataTokenUsage(metadata)
		if ok {
			*usage = &Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}
		}
		return
	}
	if (*usage).TotalTokens == 0 && ((*usage).PromptTokens != 0 || (*usage).CompletionTokens != 0) {
		(*usage).TotalTokens = (*usage).PromptTokens + (*usage).CompletionTokens
	}
}

func normalizeResponseUsage(usage **ResponseUsage, metadata map[string]any) {
	if *usage == nil {
		input, output, ok := metadataTokenUsage(metadata)
		if ok {
			*usage = &ResponseUsage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}
		}
		return
	}
	if (*usage).TotalTokens == 0 && ((*usage).InputTokens != 0 || (*usage).OutputTokens != 0) {
		(*usage).TotalTokens = (*usage).InputTokens + (*usage).OutputTokens
	}
}

func normalizeChatResponse(response *ChatCompletionResponse, headerRequestID string) {
	if response == nil {
		return
	}
	response.Metadata = metadataWithRequestID(response.Metadata, headerRequestID)
	// When orchestration split billing across the per-sub-task metadata array
	// and no protocol-level usage was provided, sum each task's billing so the
	// caller sees the whole run's token count rather than only the primary
	// entry's. Fall back to the single-object path otherwise.
	if response.Usage == nil && len(response.MetadataList) > 0 {
		if input, output, ok := metadataListTokenUsage(response.MetadataList); ok {
			response.Usage = &Usage{PromptTokens: input, CompletionTokens: output, TotalTokens: input + output}
		}
	}
	normalizeChatUsage(&response.Usage, response.Metadata)
}

func normalizeChatChunk(chunk *ChatCompletionChunk, headerRequestID string) {
	if chunk == nil {
		return
	}
	// Metadata-only Gateway events are valid SDK events. Use a non-nil empty
	// slice so callers can distinguish them without handling a nil collection.
	if chunk.Choices == nil {
		chunk.Choices = []ChatCompletionChunkChoice{}
	}
	chunk.Metadata = metadataWithRequestID(chunk.Metadata, headerRequestID)
	normalizeChatUsage(&chunk.Usage, chunk.Metadata)
}

func normalizeResponse(response *Response, headerRequestID string) {
	if response == nil {
		return
	}
	response.Metadata = metadataWithRequestID(response.Metadata, headerRequestID)
	normalizeResponseUsage(&response.Usage, response.Metadata)
}

func normalizeSuccessOutput(output any, headers http.Header) {
	requestID := requestIDFromHeaders(headers)
	switch response := output.(type) {
	case *ChatCompletionResponse:
		normalizeChatResponse(response, requestID)
	case *Response:
		normalizeResponse(response, requestID)
	case *ImageGenerationResponse:
		response.Metadata = metadataWithRequestID(response.Metadata, requestID)
	}
}
