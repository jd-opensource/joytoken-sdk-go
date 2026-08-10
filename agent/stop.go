package agent

import (
	"math"
	"strconv"

	joytoken "github.com/jd-opensource/joytoken-sdk-go"
)

// StepCountIs stops a run after maxSteps model steps.
func StepCountIs(maxSteps int) StopCondition {
	return func(state AgentState) StopDecision {
		return StopDecision{Stop: state.Step >= maxSteps, Reason: "step_count:" + itoa(maxSteps)}
	}
}

// MaxToolCalls stops a run after maxCalls tool invocations.
func MaxToolCalls(maxCalls int) StopCondition {
	return func(state AgentState) StopDecision {
		return StopDecision{Stop: state.ToolCalls >= maxCalls, Reason: "max_tool_calls:" + itoa(maxCalls)}
	}
}

// MaxCost stops a run when accumulated cost reaches maxCredits.
func MaxCost(maxCredits float64) StopCondition {
	return func(state AgentState) StopDecision {
		return StopDecision{Stop: state.Usage.Cost != nil && *state.Usage.Cost >= maxCredits, Reason: "max_cost:" + ftoa(maxCredits)}
	}
}

func shouldStop(stopWhen []StopCondition, state AgentState) string {
	for _, condition := range stopWhen {
		if condition == nil {
			continue
		}
		decision := condition(state)
		if decision.Stop {
			if decision.Reason != "" {
				return decision.Reason
			}
			return "custom"
		}
	}
	return ""
}

func emptyUsage() UsageSummary {
	return UsageSummary{}
}

func addUsage(summary UsageSummary, usage *joytoken.Usage) UsageSummary {
	if usage == nil {
		return summary
	}
	summary.PromptTokens += usage.PromptTokens
	summary.CompletionTokens += usage.CompletionTokens
	summary.TotalTokens += usage.TotalTokens
	if usage.Cost != nil || usage.TotalCost != nil || summary.Cost != nil {
		cost := 0.0
		if summary.Cost != nil {
			cost = *summary.Cost
		}
		if usage.Cost != nil {
			cost += *usage.Cost
		} else if usage.TotalCost != nil {
			cost += *usage.TotalCost
		}
		summary.Cost = &cost
	}
	return summary
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func ftoa(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
