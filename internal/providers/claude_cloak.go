package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tproxy/tproxy/internal/store"
)

// Claude Code request imitation.
//
// A Claude Code OAuth token is scoped to Claude Code. When a request carries
// that token but does not look like Claude Code traffic, Anthropic bills it as
// a third-party app ("Third-party apps now draw from your extra usage, not your
// plan limits") instead of drawing on the account's plan. Tool names are the
// loudest tell: a real client only ever declares its own fixed tool set.
//
// Everything here is applied only to Claude OAuth credentials. An API key is a
// legitimate third-party caller and its requests are forwarded untouched.
//
// The request body handed in is a *shallow* clone of the caller's raw request
// (see cloneAnyMap), so every nested value this file changes is rebuilt into a
// new slice or map rather than mutated in place. Mutating in place would
// corrupt the original request, which the router still holds for retries and
// failover to another provider.

// claudeToolSuffix is appended to caller-supplied tool names so the declared
// set is dominated by real Claude Code tool names.
const claudeToolSuffix = "_cc"

// claudeFingerprintSalt matches the reference implementations so a given
// payload produces the same build hash across tools that speak this protocol.
const claudeFingerprintSalt = "59cf53e54c78"

// claudeBillingPrefix marks a system block this gateway has already injected,
// so a retried or re-entered request is not double-prefixed.
const claudeBillingPrefix = "x-anthropic-billing-header:"

// claudeDecoyTools are the tool names a real Claude Code CLI declares. They are
// appended to every cloaked request and marked unavailable so the model never
// calls them: their only job is to make the declared tool set look native.
var claudeDecoyTools = []string{
	"Task", "TaskOutput", "TaskStop", "TaskCreate", "TaskGet", "TaskUpdate", "TaskList",
	"Bash", "Glob", "Grep", "Read", "Edit", "Write", "NotebookEdit",
	"WebFetch", "WebSearch", "AskUserQuestion", "Skill", "EnterPlanMode", "ExitPlanMode",
}

// claudeAgentPrompt is the identity line a real Claude Code CLI puts at the
// head of its system prompt, immediately after the billing block.
const claudeAgentPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

// claudeCloakRequest rewrites body in place to look like Claude Code traffic and
// returns the map that turns upstream tool names back into the caller's names.
// A nil map means no tool was renamed and responses need no translation.
func claudeCloakRequest(body map[string]any, credential store.Credential, agentPrompt bool) map[string]string {
	if body == nil {
		return nil
	}
	identity := claudeIdentityFor(credential)
	claudeInjectBillingBlock(body, agentPrompt)
	claudeInjectUserID(body, identity)
	return claudeCloakTools(body)
}

// claudeInjectBillingBlock prepends the billing attribution block that a real
// client puts at the head of its system prompt. When agentPrompt is set, the
// Claude Code identity line follows it, ahead of the caller's own system
// prompt, which the caller keeps either way.
func claudeInjectBillingBlock(body map[string]any, agentPrompt bool) {
	existing := claudeSystemBlocks(body["system"])
	if len(existing) > 0 {
		if first, ok := existing[0].(map[string]any); ok {
			if strings.HasPrefix(stringValue(first["text"]), claudeBillingPrefix) {
				return
			}
		}
	}
	blocks := make([]any, 0, len(existing)+2)
	blocks = append(blocks, map[string]any{"type": "text", "text": claudeBillingHeader(body, existing)})
	if agentPrompt {
		blocks = append(blocks, map[string]any{"type": "text", "text": claudeAgentPrompt})
	}
	blocks = append(blocks, existing...)
	body["system"] = blocks
}

// claudeSystemBlocks normalizes the system field, which the Messages API accepts
// either as a bare string or as an array of content blocks.
func claudeSystemBlocks(value any) []any {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": typed}}
	case []any:
		return append([]any(nil), typed...)
	case []map[string]any:
		blocks := make([]any, 0, len(typed))
		for _, block := range typed {
			blocks = append(blocks, block)
		}
		return blocks
	default:
		return nil
	}
}

// claudeBillingHeader renders the attribution line.
// Format: cc_version=<version>.<build>; cc_entrypoint=<entrypoint>; cch=<hash>;
// The build hash is derived from the leading system text so it is stable for a
// given conversation, and cch from the payload so it changes with the content —
// both matching what a real client produces.
func claudeBillingHeader(body map[string]any, systemBlocks []any) string {
	payload, err := json.Marshal(body)
	if err != nil {
		payload = nil
	}
	sum := sha256.Sum256(payload)
	cch := hex.EncodeToString(sum[:])[:5]
	build := claudeBuildFingerprint(claudeLeadingSystemText(systemBlocks))
	return fmt.Sprintf("%s cc_version=%s.%s; cc_entrypoint=%s; cch=%s;",
		claudeBillingPrefix, claudeCodeVersion, build, claudeCodeEntrypoint, cch)
}

func claudeLeadingSystemText(blocks []any) string {
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(block["type"]) != "text" {
			continue
		}
		return stringValue(block["text"])
	}
	return ""
}

// claudeBuildFingerprint samples three fixed character positions of the leading
// system text and hashes them with the client version.
func claudeBuildFingerprint(messageText string) string {
	runes := []rune(messageText)
	var sampled strings.Builder
	for _, index := range [3]int{4, 7, 20} {
		if index < len(runes) {
			sampled.WriteRune(runes[index])
			continue
		}
		sampled.WriteRune('0')
	}
	sum := sha256.Sum256([]byte(claudeFingerprintSalt + sampled.String() + claudeCodeVersion))
	return hex.EncodeToString(sum[:])[:3]
}

// claudeInjectUserID adds the identity blob a real client reports in request
// metadata. A caller-supplied user_id is left alone.
func claudeInjectUserID(body map[string]any, identity claudeIdentity) {
	metadata, _ := body["metadata"].(map[string]any)
	if stringValue(metadata["user_id"]) != "" {
		return
	}
	updated := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		updated[key] = value
	}
	updated["user_id"] = fmt.Sprintf(`{"device_id":"%s","account_uuid":"%s","session_id":"%s"}`,
		identity.DeviceID, identity.AccountUUID, identity.SessionID)
	body["metadata"] = updated
}

// claudeCloakTools suffixes every caller-declared tool and appends the decoy
// set. Anthropic's own server-side tools carry a "type" and must keep their
// reserved names, so they are passed through untouched.
func claudeCloakTools(body map[string]any) map[string]string {
	tools, ok := claudeToolList(body["tools"])
	if !ok || len(tools) == 0 {
		return nil
	}
	reverse := make(map[string]string, len(tools))
	cloaked := make([]any, 0, len(tools)+len(claudeDecoyTools))
	renamed := map[string]string{}
	for _, tool := range tools {
		if stringValue(tool["type"]) != "" {
			cloaked = append(cloaked, tool)
			continue
		}
		name := stringValue(tool["name"])
		if name == "" {
			cloaked = append(cloaked, tool)
			continue
		}
		suffixed := name + claudeToolSuffix
		reverse[suffixed] = name
		renamed[name] = suffixed
		cloaked = append(cloaked, claudeRenamedTool(tool, suffixed))
	}
	if len(renamed) == 0 {
		return nil
	}
	for _, decoy := range claudeDecoyTools {
		cloaked = append(cloaked, map[string]any{
			"name":         decoy,
			"description":  "This tool is currently unavailable.",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	body["tools"] = cloaked
	claudeRenameToolChoice(body, renamed)
	claudeRenameMessageToolUses(body, renamed)
	return reverse
}

func claudeRenamedTool(tool map[string]any, name string) map[string]any {
	updated := make(map[string]any, len(tool))
	for key, value := range tool {
		updated[key] = value
	}
	updated["name"] = name
	return updated
}

// claudeRenameToolChoice keeps a forced tool choice pointing at a tool that is
// still in the declared list; upstream rejects the request otherwise.
func claudeRenameToolChoice(body map[string]any, renamed map[string]string) {
	choice, ok := body["tool_choice"].(map[string]any)
	if !ok || stringValue(choice["type"]) != "tool" {
		return
	}
	suffixed, ok := renamed[stringValue(choice["name"])]
	if !ok {
		return
	}
	updated := make(map[string]any, len(choice))
	for key, value := range choice {
		updated[key] = value
	}
	updated["name"] = suffixed
	body["tool_choice"] = updated
}

// claudeRenameMessageToolUses rewrites tool_use blocks already in the
// conversation so prior turns agree with the renamed declarations.
func claudeRenameMessageToolUses(body map[string]any, renamed map[string]string) {
	messages, ok := body["messages"].([]any)
	if !ok {
		return
	}
	rebuilt := make([]any, len(messages))
	copy(rebuilt, messages)
	changed := false
	for index, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		blocks := make([]any, len(content))
		copy(blocks, content)
		blockChanged := false
		for blockIndex, blockRaw := range content {
			block, ok := blockRaw.(map[string]any)
			if !ok || stringValue(block["type"]) != "tool_use" {
				continue
			}
			suffixed, ok := renamed[stringValue(block["name"])]
			if !ok {
				continue
			}
			blocks[blockIndex] = claudeRenamedTool(block, suffixed)
			blockChanged = true
		}
		if !blockChanged {
			continue
		}
		updated := make(map[string]any, len(message))
		for key, value := range message {
			updated[key] = value
		}
		updated["content"] = blocks
		rebuilt[index] = updated
		changed = true
	}
	if changed {
		body["messages"] = rebuilt
	}
}

// claudeToolList normalizes the tools field, which reaches this point either as
// decoded JSON ([]any) or as a slice built by the request translators.
func claudeToolList(value any) ([]map[string]any, bool) {
	switch typed := value.(type) {
	case []map[string]any:
		return typed, true
	case []any:
		tools := make([]map[string]any, 0, len(typed))
		for _, raw := range typed {
			tool, ok := raw.(map[string]any)
			if !ok {
				return nil, false
			}
			tools = append(tools, tool)
		}
		return tools, true
	default:
		return nil, false
	}
}

// claudeDecloakToolName maps an upstream tool name back to the caller's name.
// Names the caller never caused us to rename pass through unchanged, so a
// caller that genuinely declared a tool ending in the suffix is unaffected.
func claudeDecloakToolName(name string, reverse map[string]string) string {
	if len(reverse) == 0 {
		return name
	}
	if original, ok := reverse[name]; ok {
		return original
	}
	return name
}

// claudeDecloakResponse restores caller tool names in a non-streaming response
// body, which is handed back to Claude-protocol callers verbatim.
func claudeDecloakResponse(raw map[string]any, reverse map[string]string) {
	if len(reverse) == 0 || raw == nil {
		return
	}
	blocks, ok := raw["content"].([]any)
	if !ok {
		return
	}
	for index, blockRaw := range blocks {
		block, ok := blockRaw.(map[string]any)
		if !ok || stringValue(block["type"]) != "tool_use" {
			continue
		}
		name := stringValue(block["name"])
		original := claudeDecloakToolName(name, reverse)
		if original == name {
			continue
		}
		blocks[index] = claudeRenamedTool(block, original)
	}
	raw["content"] = blocks
}
