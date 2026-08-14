package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	antigravityToolNameMaxLen = 64
	antigravitySchemaMaxDepth = 64
)

var antigravityToolNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.:-]{0,63}$`)

// normalizeAntigravityRequest applies the Cloud Code request contract to a
// Gemini-shaped request. It returns wire-name -> original-name mappings so an
// implementation-only renamed tool is never exposed to the caller.
func normalizeAntigravityRequest(request map[string]any) map[string]string {
	if request == nil {
		return nil
	}
	antigravityClampOutputTokens(request)
	antigravityRepairToolResponseNames(request)
	// Runs before the tool rename so signature repair sees the caller's history
	// exactly as it arrived.
	antigravityRestoreThoughtSignatures(request)
	forward, reverse := antigravityToolNameMaps(antigravityFunctionNames(request))
	antigravityNormalizeTools(request, forward)
	antigravityRewriteFunctionNames(request, forward)
	antigravityDefaultToolConfig(request)
	return reverse
}

// antigravityDefaultToolConfig asks Cloud Code to validate calls against the
// declared schemas when the caller expressed no preference. Without a mode the
// upstream is free to emit calls that do not match the declaration, which the
// client then rejects.
func antigravityDefaultToolConfig(request map[string]any) {
	if len(antigravityMapSlice(request["tools"])) == 0 {
		return
	}
	if config, ok := request["toolConfig"].(map[string]any); ok && len(config) > 0 {
		return
	}
	request["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "VALIDATED"}}
}

// antigravityRepairToolResponseNames restores a functionResponse name from
// earlier call history when an OpenAI-style tool-result message only carries a
// call ID. Cloud Code requires the response name to match the function call;
// using the opaque call ID as its name causes otherwise valid multi-turn tool
// conversations to be rejected.
func antigravityRepairToolResponseNames(request map[string]any) {
	namesByID := make(map[string]string)
	for _, content := range antigravityMapSlice(request["contents"]) {
		for _, part := range antigravityMapSlice(content["parts"]) {
			if call, ok := part["functionCall"].(map[string]any); ok {
				id := stringValue(firstValue(call, "id", "call_id"))
				name := stringValue(call["name"])
				if id != "" && name != "" {
					namesByID[id] = name
				}
			}
			if response, ok := part["functionResponse"].(map[string]any); ok {
				id := stringValue(firstValue(response, "id", "call_id"))
				name := stringValue(response["name"])
				if expected := namesByID[id]; expected != "" && (name == "" || name == id) {
					response["name"] = expected
				}
			}
		}
	}
}

func cloneAntigravityMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		// geminiBody only creates JSON values. Do not mutate a programmatic
		// caller's map if a non-JSON value somehow reaches this boundary.
		return map[string]any{}
	}
	var cloned map[string]any
	if json.Unmarshal(encoded, &cloned) != nil || cloned == nil {
		return map[string]any{}
	}
	return cloned
}

func antigravityClampOutputTokens(request map[string]any) {
	generation, ok := request["generationConfig"].(map[string]any)
	if !ok || generation == nil {
		return
	}
	if max, ok := antigravityOutputTokens(generation["maxOutputTokens"]); ok && max > antigravityMaxOutputTokens {
		generation["maxOutputTokens"] = antigravityMaxOutputTokens
	}
}

// antigravityOutputTokens accepts the numeric JSON forms the Gemini protocol
// permits without treating an unparseable value as zero. The latter would let
// a string-encoded oversized limit bypass the Cloud Code ceiling.
func antigravityOutputTokens(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case float64:
		// Do not cast before checking bounds: Go's float-to-int conversion is
		// implementation-dependent for values outside int64's range.
		parsed, err := strconv.ParseInt(strconv.FormatFloat(typed, 'f', -1, 64), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func antigravityFunctionNames(request map[string]any) []string {
	var names []string
	for _, group := range antigravityMapSlice(request["tools"]) {
		for _, declaration := range antigravityMapSlice(firstAny(group["functionDeclarations"], group["function_declarations"])) {
			if name := stringValue(declaration["name"]); name != "" {
				names = append(names, name)
			}
		}
	}
	for _, content := range antigravityMapSlice(request["contents"]) {
		for _, part := range antigravityMapSlice(content["parts"]) {
			for _, key := range []string{"functionCall", "functionResponse"} {
				if function, ok := part[key].(map[string]any); ok {
					if name := stringValue(function["name"]); name != "" {
						names = append(names, name)
					}
				}
			}
		}
	}
	if config, ok := request["toolConfig"].(map[string]any); ok {
		if calling, ok := config["functionCallingConfig"].(map[string]any); ok {
			names = append(names, antigravityStrings(calling["allowedFunctionNames"])...)
		}
	}
	return names
}

func antigravityNormalizeTools(request map[string]any, forward map[string]string) {
	groups := antigravityMapSlice(request["tools"])
	if len(groups) == 0 {
		return
	}
	result := make([]any, 0, len(groups)+1)
	declarations := make([]any, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		functionDeclarations := antigravityMapSlice(firstAny(group["functionDeclarations"], group["function_declarations"]))
		// Preserve native Gemini tools such as googleSearch. A group may carry
		// both a native tool and function declarations; move only the latter to
		// the consolidated declaration group so the native capability is not
		// silently discarded.
		native := antigravityNativeToolGroup(group)
		if len(native) > 0 {
			result = append(result, native)
		}
		for _, declaration := range functionDeclarations {
			name := stringValue(declaration["name"])
			wireName := antigravityWireToolName(name, forward)
			if wireName == "" {
				continue
			}
			if _, exists := seen[wireName]; exists {
				continue
			}
			seen[wireName] = struct{}{}
			clean := map[string]any{"name": wireName}
			if description := strings.TrimSpace(stringValue(declaration["description"])); description != "" {
				clean["description"] = description
			}
			if schema := firstAny(declaration["parameters"], declaration["parametersJsonSchema"], declaration["input_schema"]); schema != nil {
				if normalized := antigravityCleanSchema(schema); normalized != nil {
					clean["parameters"] = normalized
				}
			}
			if _, present := clean["parameters"]; !present {
				clean["parameters"] = antigravityPlaceholderToolSchema()
			}
			declarations = append(declarations, clean)
		}
	}
	if len(declarations) > 0 {
		result = append(result, map[string]any{"functionDeclarations": declarations})
	}
	if len(result) == 0 {
		delete(request, "tools")
		return
	}
	request["tools"] = result
}

func antigravityNativeToolGroup(group map[string]any) map[string]any {
	native := make(map[string]any, len(group))
	for key, value := range group {
		if key == "functionDeclarations" || key == "function_declarations" {
			continue
		}
		native[key] = value
	}
	return native
}

func antigravityRewriteFunctionNames(request map[string]any, forward map[string]string) {
	if len(forward) == 0 {
		return
	}
	for _, content := range antigravityMapSlice(request["contents"]) {
		for _, part := range antigravityMapSlice(content["parts"]) {
			for _, key := range []string{"functionCall", "functionResponse"} {
				if function, ok := part[key].(map[string]any); ok {
					function["name"] = antigravityWireToolName(stringValue(function["name"]), forward)
				}
			}
		}
	}
	if config, ok := request["toolConfig"].(map[string]any); ok {
		if calling, ok := config["functionCallingConfig"].(map[string]any); ok {
			if allowed := antigravityStrings(calling["allowedFunctionNames"]); len(allowed) > 0 {
				mapped := make([]any, 0, len(allowed))
				for _, name := range allowed {
					mapped = append(mapped, antigravityWireToolName(name, forward))
				}
				calling["allowedFunctionNames"] = mapped
			}
		}
	}
}

func antigravityToolNameMaps(names []string) (map[string]string, map[string]string) {
	forward := make(map[string]string)
	reverse := make(map[string]string)
	unique := make(map[string]struct{})
	counts := make(map[string]int)
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, exists := unique[name]; exists {
			continue
		}
		unique[name] = struct{}{}
		counts[antigravitySanitizeToolName(name)]++
	}
	sorted := make([]string, 0, len(unique))
	for name := range unique {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	used := make(map[string]struct{}, len(sorted))
	for _, name := range sorted {
		base := antigravitySanitizeToolName(name)
		wireName := base
		if counts[base] > 1 {
			wireName = antigravityDisambiguateToolName(base, name, used)
		}
		if _, exists := used[wireName]; exists {
			wireName = antigravityDisambiguateToolName(base, name, used)
		}
		used[wireName] = struct{}{}
		forward[name] = wireName
		reverse[wireName] = name
	}
	return forward, reverse
}

func antigravityWireToolName(name string, forward map[string]string) string {
	if wireName := forward[name]; wireName != "" {
		return wireName
	}
	if name == "" {
		return ""
	}
	return antigravitySanitizeToolName(name)
}

func restoreAntigravityToolName(name string, reverse map[string]string) string {
	if original := reverse[name]; original != "" {
		return original
	}
	return name
}

func antigravitySanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if antigravityToolNamePattern.MatchString(name) {
		return name
	}
	var out strings.Builder
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '.' || character == ':' || character == '-' {
			out.WriteRune(character)
		} else {
			out.WriteByte('_')
		}
	}
	result := out.String()
	if result == "" {
		result = "tool"
	}
	first := result[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		result = "tool_" + result
	}
	if len(result) > antigravityToolNameMaxLen {
		result = result[:antigravityToolNameMaxLen]
	}
	return result
}

func antigravityDisambiguateToolName(base, original string, used map[string]struct{}) string {
	for attempt := 0; ; attempt++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", original, attempt)))
		suffix := "_" + hex.EncodeToString(digest[:6])
		prefixLen := antigravityToolNameMaxLen - len(suffix)
		if prefixLen < 1 {
			prefixLen = 1
		}
		prefix := base
		if len(prefix) > prefixLen {
			prefix = prefix[:prefixLen]
		}
		candidate := prefix + suffix
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func antigravityCleanSchema(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var decoded any
	if json.Unmarshal(encoded, &decoded) != nil {
		return map[string]any{"type": "object"}
	}
	cleaned := antigravityCleanSchemaValue(decoded, 0)
	if schema, ok := cleaned.(map[string]any); ok {
		return schema
	}
	return map[string]any{"type": "object"}
}

func antigravityCleanSchemaValue(value any, depth int) any {
	if depth >= antigravitySchemaMaxDepth {
		return map[string]any{"type": "object"}
	}
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if reference := strings.TrimSpace(stringValue(schema["$ref"])); reference != "" {
		name := reference
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			name = name[slash+1:]
		}
		result := map[string]any{"type": "object"}
		if name != "" {
			result["description"] = "See: " + name
		}
		return result
	}
	// Gemini requires string enum values. Convert const before dropping its
	// JSON-Schema-only keyword so literal OpenAPI/Zod schemas retain their
	// useful constraint instead of becoming unconstrained fields.
	if _, hasEnum := schema["enum"]; !hasEnum {
		if constant, hasConst := schema["const"]; hasConst {
			schema["enum"] = []any{constant}
		}
	}

	result := make(map[string]any, len(schema))
	for key, raw := range schema {
		switch key {
		case "$schema", "$defs", "definitions", "const", "$ref", "$id", "additionalProperties",
			"propertyNames", "patternProperties", "$comment", "enumDescriptions", "enumTitles",
			"prefill", "deprecated", "readOnly", "writeOnly", "title", "optional",
			"minLength", "maxLength", "exclusiveMinimum", "exclusiveMaximum", "pattern", "minItems",
			"maxItems", "uniqueItems", "format", "default", "examples", "allOf", "anyOf", "oneOf",
			"not", "dependencies", "dependentSchemas", "dependentRequired", "if", "then", "else",
			"contentMediaType", "contentEncoding", "prefixItems", "additionalItems", "contains",
			"minContains", "maxContains", "unevaluatedItems", "unevaluatedProperties", "discriminator",
			"cornerRadius", "fillColor", "fontFamily", "fontSize", "fontWeight", "gap", "padding",
			"strokeColor", "strokeThickness", "textColor":
			continue
		case "properties":
			properties := make(map[string]any)
			if object, ok := raw.(map[string]any); ok {
				for propertyName, propertySchema := range object {
					properties[propertyName] = antigravityCleanSchemaValue(propertySchema, depth+1)
				}
			}
			result[key] = properties
		case "items":
			if items := antigravityCleanSchemaItems(raw, depth+1); items != nil {
				result[key] = items
			}
		case "type":
			result[key] = antigravitySchemaType(raw)
		case "enum":
			if values, ok := raw.([]any); ok {
				converted := make([]any, 0, len(values))
				for _, item := range values {
					converted = append(converted, stringValue(item))
				}
				result[key] = converted
				result["type"] = "string"
			}
		default:
			if strings.HasPrefix(key, "x-") {
				continue
			}
			result[key] = antigravityCleanSchemaValue(raw, depth+1)
		}
	}

	antigravityMergeAllOf(result, schema["allOf"], depth)
	if alternative := antigravityBestAlternative(firstAny(schema["anyOf"], schema["oneOf"])); alternative != nil {
		best, _ := antigravityCleanSchemaValue(alternative, depth+1).(map[string]any)
		if best != nil {
			if description := strings.TrimSpace(stringValue(result["description"])); description != "" && strings.TrimSpace(stringValue(best["description"])) == "" {
				best["description"] = description
			}
			return best
		}
	}
	antigravityCleanRequired(result)
	if _, exists := result["type"]; !exists {
		if _, hasProperties := result["properties"]; hasProperties {
			result["type"] = "object"
		}
	}
	return result
}

// antigravityCleanSchemaItems turns JSON Schema's tuple form (`items: [...]`)
// into the single schema Gemini accepts. Prefer the most expressive usable
// tuple member, following the same selection rule as anyOf/oneOf.
func antigravityCleanSchemaItems(value any, depth int) any {
	if alternatives := antigravityAnySlice(value); len(alternatives) > 0 {
		if best := antigravityBestAlternative(alternatives); best != nil {
			if cleaned, ok := antigravityCleanSchemaValue(best, depth).(map[string]any); ok {
				return cleaned
			}
		}
		return nil
	}
	if cleaned, ok := antigravityCleanSchemaValue(value, depth).(map[string]any); ok {
		return cleaned
	}
	return nil
}

func antigravityMergeAllOf(result map[string]any, value any, depth int) {
	items, _ := value.([]any)
	for _, item := range items {
		part, _ := antigravityCleanSchemaValue(item, depth+1).(map[string]any)
		if part == nil {
			continue
		}
		if result["type"] == nil && part["type"] != nil {
			result["type"] = part["type"]
		}
		if properties, ok := part["properties"].(map[string]any); ok {
			current, _ := result["properties"].(map[string]any)
			if current == nil {
				current = make(map[string]any)
				result["properties"] = current
			}
			for name, property := range properties {
				current[name] = property
			}
		}
		if required := antigravityStrings(part["required"]); len(required) > 0 {
			merged := append(antigravityStrings(result["required"]), required...)
			result["required"] = antigravityUniqueStrings(merged)
		}
	}
}

func antigravityBestAlternative(value any) any {
	items := antigravityAnySlice(value)
	if len(items) == 0 {
		return nil
	}
	best := items[0]
	bestScore := -1
	for _, item := range items {
		schema, _ := item.(map[string]any)
		score := 0
		switch {
		case schema == nil:
			score = 0
		case schema["properties"] != nil || stringValue(schema["type"]) == "object":
			score = 3
		case schema["items"] != nil || stringValue(schema["type"]) == "array":
			score = 2
		case stringValue(schema["type"]) != "null":
			score = 1
		}
		if score > bestScore {
			best, bestScore = item, score
		}
	}
	return best
}

func antigravitySchemaType(value any) string {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			return typed
		}
	case []any:
		for _, item := range typed {
			if name := stringValue(item); name != "" && name != "null" {
				return name
			}
		}
	}
	return "string"
}

func antigravityCleanRequired(schema map[string]any) {
	required := antigravityStrings(schema["required"])
	if len(required) == 0 {
		delete(schema, "required")
		return
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		delete(schema, "required")
		return
	}
	valid := make([]string, 0, len(required))
	for _, name := range antigravityUniqueStrings(required) {
		if _, exists := properties[name]; exists {
			valid = append(valid, name)
		}
	}
	if len(valid) == 0 {
		delete(schema, "required")
		return
	}
	schema["required"] = valid
}

func antigravityMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok && mapped != nil {
				result = append(result, mapped)
			}
		}
		return result
	case []map[string]any:
		return typed
	default:
		return nil
	}
}

func antigravityAnySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = typed[index]
		}
		return result
	default:
		return nil
	}
}

func antigravityStrings(value any) []string {
	var result []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if name := stringValue(item); name != "" {
				result = append(result, name)
			}
		}
	case []string:
		for _, item := range typed {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func antigravityUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists || value == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
