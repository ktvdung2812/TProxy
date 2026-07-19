package guardrails

import (
	"regexp"
	"strings"
)

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore (all )?(previous|prior|above) instructions`),
	regexp.MustCompile(`(?i)disregard (your|the) (system|safety) (prompt|instructions)`),
	regexp.MustCompile(`(?i)you are now (in )?(developer|admin|god|jailbreak) mode`),
	regexp.MustCompile(`(?i)reveal (your|the) (system|hidden) prompt`),
	regexp.MustCompile(`(?i)print (the )?(system|hidden) (prompt|instructions)`),
	regexp.MustCompile(`(?i)bypass (content|safety) (policy|filter|guard)`),
}

// DetectInjection returns true when user content matches known injection heuristics.
func DetectInjection(messages []string) bool {
	for _, message := range messages {
		trimmed := strings.TrimSpace(message)
		if trimmed == "" {
			continue
		}
		for _, pattern := range injectionPatterns {
			if pattern.MatchString(trimmed) {
				return true
			}
		}
	}
	return false
}
