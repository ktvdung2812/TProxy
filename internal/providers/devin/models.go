package devin

import (
	"strings"
	"sync"
)

// effortSuffixes are the thinking-effort suffixes Devin appends to base model
// UIDs. A UID already carrying one passes through resolution unchanged.
var effortSuffixes = []string{
	"-none", "-minimal", "-low", "-medium", "-high", "-xhigh", "-max",
	"-fast", "-slow", "-priority",
}

// staticModelLevels maps base model UIDs to the effort levels the upstream
// catalog offers, as observed from GetCliModelConfigs. Live discovery refreshes
// this table via RegisterModelLevels.
var staticModelLevels = map[string][]string{
	"claude-opus-5":       {"low", "medium", "high", "xhigh", "max", "low-fast", "medium-fast", "high-fast", "xhigh-fast", "max-fast"},
	"claude-opus-4-8":     {"low", "medium", "high", "xhigh", "max", "low-fast", "medium-fast", "high-fast", "xhigh-fast", "max-fast"},
	"claude-opus-4-7":     {"low", "medium", "high", "xhigh", "max"},
	"claude-sonnet-5":     {"low", "medium", "high", "xhigh", "max"},
	"claude-5-fable":      {"low", "medium", "high", "xhigh", "max"},
	"claude-fable-5-1":    {"low", "medium", "high", "xhigh", "max"},
	"gemini-3-5-flash":    {"minimal", "low", "medium", "high"},
	"gemini-3-6-flash":    {"minimal", "low", "medium", "high"},
	"gemini-3-7-flash":    {"low", "medium", "high"},
	"gemini-3-8-flash":    {"low", "medium", "high"},
	"gemini-3-1-pro":      {"low", "high"},
	"gpt-5-3-codex":       {"low", "medium", "high", "xhigh", "low-priority", "medium-priority", "high-priority", "xhigh-priority"},
	"gpt-5-4":             {"none", "low", "medium", "high", "xhigh", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "none-priority"},
	"gpt-5-4-mini":        {"low", "medium", "high", "xhigh"},
	"gpt-5-5":             {"none", "low", "medium", "high", "xhigh", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "none-priority"},
	"gpt-5-6-sol":         {"none", "low", "medium", "high", "xhigh", "max", "none-priority", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "max-priority"},
	"gpt-5-6-luna":        {"none", "low", "medium", "high", "xhigh", "max", "none-priority", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "max-priority"},
	"gpt-5-6-terra":       {"none", "low", "medium", "high", "xhigh", "max", "none-priority", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "max-priority"},
	"gpt-6-astra":         {"low", "medium", "high", "xhigh", "max", "low-priority", "medium-priority", "high-priority", "xhigh-priority", "max-priority"},
	"grok-4-5":            {"low", "medium", "high"},
	"grok-4-6":            {"low", "medium", "high", "xhigh"},
	"inkling":             {"none", "low", "medium", "high", "xhigh", "max"},
	"kimi-k3":             {"low", "high", "max"},
	"glm-5-3":             {"low", "high", "max"},
	"glm-5-3-flash":       {"low", "high", "max"},
	"glm-5-2":             {"none", "max"},
	"deepseek-v4-pro":     {"high", "max"},
	"deepseek-v4-flash":   {"high", "max"},
	"deepseek-v4-1-flash": {"high", "max"},
	"nemotron-3-ultra":    {"none", "medium", "high"},
	"swe-1-7":             {"medium", "lightning", "lightning-medium"},
	"swe-1-7-lightning":   {"medium"},
	"swe-1-6":             {"fast"},
	"swe-2":               {"medium", "high", "max"},
}

var modelLevels = struct {
	sync.RWMutex
	base map[string][]string
}{base: staticModelLevels}

// RegisterModelLevels merges live-discovered catalog data into the level
// table: each UID's effort suffix is split off and collected per base model.
func RegisterModelLevels(uids []string) {
	merged := map[string][]string{}
	for k, v := range staticModelLevels {
		merged[k] = append([]string(nil), v...)
	}
	seen := map[string]map[string]bool{}
	for base, lv := range merged {
		seen[base] = map[string]bool{}
		for _, l := range lv {
			seen[base][l] = true
		}
	}
	for _, uid := range uids {
		base, level := splitEffort(uid)
		if level == "" {
			continue
		}
		if seen[base] == nil {
			seen[base] = map[string]bool{}
		}
		if !seen[base][level] {
			seen[base][level] = true
			merged[base] = append(merged[base], level)
		}
	}
	modelLevels.Lock()
	modelLevels.base = merged
	modelLevels.Unlock()
}

// splitEffort splits "claude-opus-5-high" into ("claude-opus-5", "high").
// Multi-word suffixes (low-fast, xhigh-priority) are handled by longest match.
func splitEffort(uid string) (base, level string) {
	for _, s := range []string{
		"-xhigh-priority", "-high-priority", "-medium-priority", "-low-priority", "-max-priority", "-none-priority",
		"-low-fast", "-medium-fast", "-high-fast", "-xhigh-fast", "-max-fast",
		"-lightning-medium", "-lightning",
		"-minimal", "-medium", "-xhigh", "-none", "-low", "-high", "-max", "-fast", "-slow", "-priority",
	} {
		if strings.HasSuffix(uid, s) {
			return strings.TrimSuffix(uid, s), strings.TrimPrefix(s, "-")
		}
	}
	return uid, ""
}

// HasEffortSuffix reports whether the model UID already ends with an effort suffix.
func HasEffortSuffix(model string) bool {
	_, level := splitEffort(strings.ToLower(strings.TrimSpace(model)))
	return level != ""
}

var specialAliases = map[string]string{
	"claude-haiku-4-5": "MODEL_PRIVATE_11",
	"gpt-4-1":          "MODEL_CHAT_GPT_4_1_2025_04_14",
}

var levelOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func levelIndex(level string) int {
	for i, l := range levelOrder {
		if l == level {
			return i
		}
	}
	return -1
}

// ResolveChatModelUID maps a friendly model name plus an optional effort
// ("low", "high", "max", …) to an upstream chat_model_uid. Resolution rules:
//  1. strip a "devin/" prefix
//
// 2. UIDs already carrying an effort suffix pass through
// 3. "model:effort" suffix syntax sets the effort
// 4. special private aliases
// 5. base models with no known levels pass through unchanged
// 6. otherwise clamp the effort to the base's allowed levels and append it
func ResolveChatModelUID(rawModel, effort string) string {
	model := strings.TrimSpace(rawModel)
	if model == "" {
		return "swe-2-high"
	}
	clean := model
	if strings.HasPrefix(strings.ToLower(clean), "devin/") {
		clean = clean[6:]
	}
	if HasEffortSuffix(clean) {
		return clean
	}
	if idx := strings.LastIndex(clean, ":"); idx != -1 {
		effort = strings.TrimSpace(clean[idx+1:])
		clean = strings.TrimSpace(clean[:idx])
	}
	base := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(clean)), ".", "-")
	if alias, ok := specialAliases[base]; ok {
		return alias
	}
	if base == "claude-sonnet-4-5" || strings.Contains(base, "sonnet-4-5") {
		if e := normalizeEffort(effort); e != "" && e != "none" {
			return "MODEL_PRIVATE_3"
		}
		return "MODEL_PRIVATE_2"
	}
	if base == "gemini-3-flash" {
		base = "gemini-3-8-flash"
	}

	// Special bare bases that only resolve for specific efforts.
	switch base {
	case "swe-1-7":
		if normalizeEffort(effort) == "medium" {
			return "swe-1-7-medium"
		}
		return "swe-1-7"
	case "swe-1-6":
		if normalizeEffort(effort) == "fast" {
			return "swe-1-6-fast"
		}
		return "swe-1-6"
	case "glm-5-2":
		switch normalizeEffort(effort) {
		case "none":
			return "glm-5-2-none"
		case "max":
			return "glm-5-2-max"
		}
		return "glm-5-2"
	}

	modelLevels.RLock()
	allowed := append([]string(nil), modelLevels.base[base]...)
	modelLevels.RUnlock()
	if len(allowed) == 0 {
		return base
	}
	return base + "-" + clampEffort(normalizeEffort(effort), allowed, defaultEffort(base, allowed))
}

func normalizeEffort(effort string) string {
	switch e := strings.ToLower(strings.TrimSpace(effort)); e {
	case "minimal", "low", "medium", "high", "xhigh", "max", "fast":
		return e
	case "none", "off", "disabled":
		return "none"
	case "auto", "adaptive":
		return "high"
	default:
		return ""
	}
}

func defaultEffort(base string, levels []string) string {
	if strings.Contains(base, "swe-2") {
		return "high"
	}
	has := func(want string) bool {
		for _, l := range levels {
			if l == want {
				return true
			}
		}
		return false
	}
	if has("none") && has("low") && strings.HasPrefix(base, "gpt-5") {
		return "low"
	}
	if has("high") && (strings.Contains(base, "gemini") || strings.Contains(base, "grok") ||
		strings.Contains(base, "glm") || strings.Contains(base, "deepseek") ||
		strings.Contains(base, "kimi") || strings.Contains(base, "nemotron")) {
		return "high"
	}
	for _, cand := range []string{"medium", "high", "low"} {
		if has(cand) {
			return cand
		}
	}
	return levels[0]
}

// clampEffort picks the closest allowed level to the requested effort,
// preferring the higher level on ties.
func clampEffort(requested string, allowed []string, fallback string) string {
	if requested == "" || requested == "none" {
		return fallback
	}
	for _, a := range allowed {
		if requested == a {
			return a
		}
	}
	reqIdx := levelIndex(requested)
	if reqIdx == -1 {
		return fallback
	}
	best, bestDist, bestIdx := fallback, 1<<30, -1
	for _, a := range allowed {
		aIdx := levelIndex(a)
		if aIdx == -1 {
			continue
		}
		dist := reqIdx - aIdx
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist || (dist == bestDist && aIdx > bestIdx) {
			best, bestDist, bestIdx = a, dist, aIdx
		}
	}
	return best
}
