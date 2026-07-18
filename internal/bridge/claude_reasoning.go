package bridge

import "strings"

type ReasoningEffortOverrides map[Role]string

var ReasoningEffortOptions = []string{"none", "low", "medium", "high", "xhigh", "max"}

func NormalizeReasoningEffort(effort string) string {
	trimmed := strings.TrimSpace(strings.ToLower(effort))
	switch trimmed {
	case "", "default", "inherit":
		return ""
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return trimmed
	default:
		return ""
	}
}

// CodexWireReasoningEffort maps UI/config effort labels to Codex API values.
func CodexWireReasoningEffort(effort string) string {
	return effort
}

// ReasoningEffortOptionsForTarget returns selectable effort levels for a mapping target.
func ReasoningEffortOptionsForTarget(target string) []string {
	trimmed := strings.TrimSpace(strings.ToLower(target))
	options := []string{"", "none", "low", "medium", "high", "xhigh"}
	if trimmed == "" || strings.Contains(trimmed, "gpt-5.6") {
		options = append(options, "max")
	}
	return options
}

func cloneReasoningEffortOverrides(overrides ReasoningEffortOverrides) ReasoningEffortOverrides {
	if len(overrides) == 0 {
		return ReasoningEffortOverrides{}
	}
	out := make(ReasoningEffortOverrides, len(overrides))
	for role, value := range overrides {
		if normalized := NormalizeReasoningEffort(value); normalized != "" {
			out[role] = normalized
		}
	}
	return out
}
