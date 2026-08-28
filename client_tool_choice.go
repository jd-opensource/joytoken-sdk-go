package joytoken

import "strings"

// continuationToolChoice keeps the complete tool declaration set available on
// continuation turns while relaxing a one-shot forced choice after the model has
// emitted a usable tool call. Without this transition, "required" or a named
// function choice can force the same tool forever and prevent a final answer.
func continuationToolChoice(choice any) any {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "required", "any":
			return "auto"
		default:
			return choice
		}
	case MessageToolChoice:
		if isForcedToolChoiceType(value.Type) {
			return MessageToolChoice{Type: "auto"}
		}
		return choice
	case *MessageToolChoice:
		if value != nil && isForcedToolChoiceType(value.Type) {
			return &MessageToolChoice{Type: "auto"}
		}
		return choice
	case map[string]any:
		typeName, _ := value["type"].(string)
		if isForcedToolChoiceType(typeName) {
			return "auto"
		}
		return choice
	default:
		return choice
	}
}

func isForcedToolChoiceType(typeName string) bool {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "function", "tool", "required", "any":
		return true
	default:
		return false
	}
}
