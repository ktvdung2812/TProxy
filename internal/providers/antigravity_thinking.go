package providers

import "strings"

// Gemini 3 and newer reject a functionCall part that carries no
// thoughtSignature. The signature is minted by the model alongside the call,
// but no client persists it: Claude Code, the OpenAI-shaped clients and the
// Antigravity IDE all replay tool history without it. Every multi-turn tool
// conversation therefore fails on its second request unless the gateway puts a
// signature back.
//
// This is the signature the Antigravity IDE itself replays, taken from
// 9router's defaultThinkingSignature table, and it is accepted for any model on
// the Antigravity surface.
const antigravityDefaultThoughtSignature = "EuwGCukGAXLI2nxwZIq54WWSoL/YN0P3TsDZ7zRnLi8g0S4aVr2HUGxvaHKySuY6HAVzcE0GPGjXrytLIldxthSvfxgU" +
	"lJh6Qa9Z+Oj5QZBlYdg6HaJ6yuY5R7waE6rdwBsRf7Ft2j3DJ9rMi9qhWFqApewYtPhls3VHtuvND3l8Rm09+lbAXQs6" +
	"KKWEWrxNLKTBkfpMgXhRERc/TQRMZu1twAablm6/Zk1tsYRvfWKLsNbeKF+CCojJdXJKvnR/8Ouuoa+Y2Ti20hcW7aZI" +
	"IjZDFYPU//k6Ybmhg69J/imbFai2ckhfLaisqdDkdoIiBJScTOUvYqP6AE9d4MsydSC+UlhIMk4hoP76R8vUSCZRMkjO" +
	"aDXstf/QoVZKbt94wyRZgAJ1G0BqI8L5ow86kLpA4wJEtxsRGymOE4bKUvApveBakYDNM9APkf+LbtbzWSseGjoZcSly" +
	"cF9iN8Q2XNYKRrHbv3Lr5Y8JjdH/5y/6SHkNehTEZugaeGnSPSyCTWto1kQgHpxdWmhkLfJGNUGLmue7Mesj4TSms4J3" +
	"3mRpYVhNB/J333FCqIP0hr/E7BkkjEn7yZ4X7SQlh+xKPurapsnHRwiKmtsilmEFrnTE9iQr+pMr6M29qqFNv1tr5yum" +
	"baJw8JW9sB15tNsRv+dW6BjNanbsKz7HCgKUBc8tGy+7YuhXzAfViyRefcjK7eZW0Fbyt7AbybJTKz78W8NH7ye6LAwz" +
	"OebXpeZ4D43fNIt8bKh26qgduSQv/7o+pAflkuqHZ99YWgHQ8h8OkZFi3eOiSYjsjhdZ/czWOdoPI/OnqIldzMPF5Ylr" +
	"KBLFX8VhRKVmqgsmWf5PHGulHhMkVlS+XG2UIseGy69ARa93D78Gsa+1n1kJr7EEB7Rh+27vUMxVYLdz1yMSvE5nalTA" +
	"lg/ZeG8+XQ0cHuAI3KbQpHW2Q++RdXfm5JzD5WdJZUU+Zn8t8UUn85BH4RxZLeE0qJikgSsKoYVBc6YhiMjhPgkR95Re" +
	"imY4Z0xCJdRo1gjexOFeODZMpQF6Yxnoic7IrdgsFA3iePTbFnPp3IAM1fAThWhXJUn3QInUOTd5o1qmTmn6REbL15g/" +
	"JQNl+dqUoPkhleeb2V3kjqp1okmO3wMZbPknR3S1LZNmlS72/iBQUm+n2b/RCn4PjmM2"

// antigravityRestoreThoughtSignatures prepares tool history for Gemini 3+:
// parts that exist only to carry reasoning are dropped, and any surviving
// functionCall without a signature is given the default one.
func antigravityRestoreThoughtSignatures(request map[string]any) {
	for _, content := range antigravityMapSlice(request["contents"]) {
		parts := antigravityMapSlice(content["parts"])
		if len(parts) == 0 {
			continue
		}
		kept := make([]any, 0, len(parts))
		for _, part := range parts {
			if antigravityThoughtOnlyPart(part) {
				continue
			}
			if call, ok := part["functionCall"].(map[string]any); ok && call != nil {
				if strings.TrimSpace(stringValue(firstValue(part, "thoughtSignature", "thought_signature"))) == "" {
					part["thoughtSignature"] = antigravityDefaultThoughtSignature
				}
			}
			kept = append(kept, part)
		}
		content["parts"] = kept
	}
}

// antigravityThoughtOnlyPart reports whether a part carries nothing the upstream
// can act on. Reasoning text replayed from a previous turn has no meaning to
// Cloud Code and a signature without its call is rejected outright.
func antigravityThoughtOnlyPart(part map[string]any) bool {
	if part == nil {
		return true
	}
	_, hasCall := part["functionCall"].(map[string]any)
	if hasCall {
		return false
	}
	if thought, _ := part["thought"].(bool); thought {
		return true
	}
	hasSignature := strings.TrimSpace(stringValue(firstValue(part, "thoughtSignature", "thought_signature"))) != ""
	if hasSignature && strings.TrimSpace(stringValue(part["text"])) == "" {
		return true
	}
	return false
}

// antigravityPlaceholderToolSchema stands in for a function declaration that
// arrives without parameters. Cloud Code rejects a declaration whose schema is
// missing or empty, so a parameterless tool would take the whole request down
// with it; a single optional field keeps the declaration valid while leaving
// the tool effectively argument-free.
func antigravityPlaceholderToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "Brief explanation",
			},
		},
		"required": []any{"reason"},
	}
}
