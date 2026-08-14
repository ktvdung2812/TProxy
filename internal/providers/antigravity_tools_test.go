package providers

import (
	"math"
	"testing"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/store"
)

func TestAntigravityCleanSchemaRemovesUnsupportedKeywords(t *testing.T) {
	cleaned, ok := antigravityCleanSchema(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "Tool input",
		"type":    "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"const":             float64(7),
				"title":             "Mode",
				"readOnly":          true,
				"writeOnly":         true,
				"not":               map[string]any{"type": "null"},
				"dependencies":      map[string]any{"other": []any{"name"}},
				"dependentSchemas":  map[string]any{"other": map[string]any{"type": "string"}},
				"dependentRequired": map[string]any{"other": []any{"name"}},
				"if":                map[string]any{"type": "string"},
				"then":              map[string]any{"minLength": float64(1)},
				"else":              map[string]any{"maxLength": float64(20)},
				"contentEncoding":   "base64",
				"contentMediaType":  "application/json",
			},
			"tuple": map[string]any{
				"type": "array",
				"items": []any{
					map[string]any{"type": "null"},
					map[string]any{"type": "string", "title": "first usable tuple item"},
				},
				"prefixItems":     []any{map[string]any{"type": "string"}},
				"additionalItems": false,
			},
		},
	}).(map[string]any)
	if !ok {
		t.Fatalf("cleaned schema has type %T", cleaned)
	}

	mode := antigravitySchemaProperty(t, cleaned, "mode")
	if mode["type"] != "string" {
		t.Fatalf("const type = %#v, want string", mode["type"])
	}
	enum, ok := mode["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "7" {
		t.Fatalf("const enum = %#v, want [\"7\"]", mode["enum"])
	}

	tuple := antigravitySchemaProperty(t, cleaned, "tuple")
	items, ok := tuple["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Fatalf("tuple items = %#v, want a string schema", tuple["items"])
	}

	unsupported := map[string]struct{}{
		"$schema": {}, "title": {}, "readOnly": {}, "writeOnly": {}, "not": {},
		"dependencies": {}, "dependentSchemas": {}, "dependentRequired": {},
		"if": {}, "then": {}, "else": {}, "contentEncoding": {},
		"contentMediaType": {}, "prefixItems": {}, "additionalItems": {}, "const": {},
	}
	if key := antigravityUnsupportedSchemaKey(cleaned, unsupported); key != "" {
		t.Fatalf("unsupported schema keyword %q survived: %#v", key, cleaned)
	}
}

func TestAntigravityNormalizeToolsPreservesNativeToolInMixedGroup(t *testing.T) {
	request := map[string]any{
		"tools": []any{map[string]any{
			"functionDeclarations": []any{map[string]any{
				"name":       "lookup",
				"parameters": map[string]any{"type": "object"},
			}},
			"googleSearch": map[string]any{},
		}},
	}

	normalizeAntigravityRequest(request)
	groups := antigravityMapSlice(request["tools"])
	var nativePreserved, functionPreserved bool
	for _, group := range groups {
		if _, ok := group["googleSearch"]; ok {
			nativePreserved = true
			if _, hasFunctions := group["functionDeclarations"]; hasFunctions {
				t.Fatalf("native group retained function declarations: %#v", group)
			}
		}
		for _, declaration := range antigravityMapSlice(group["functionDeclarations"]) {
			if declaration["name"] == "lookup" {
				functionPreserved = true
			}
		}
	}
	if !nativePreserved || !functionPreserved {
		t.Fatalf("normalized tools lost a native or function tool: %#v", request["tools"])
	}
}

func TestAntigravityToolNameMapsDisambiguateSanitizedCollisions(t *testing.T) {
	forward, reverse := antigravityToolNameMaps([]string{"search tool", "search/tool"})
	first, second := forward["search tool"], forward["search/tool"]
	if first == "" || second == "" || first == second {
		t.Fatalf("collision was not disambiguated: %#v", forward)
	}
	if !antigravityToolNamePattern.MatchString(first) || !antigravityToolNamePattern.MatchString(second) {
		t.Fatalf("invalid wire names: %#v", forward)
	}
	if reverse[first] != "search tool" || reverse[second] != "search/tool" {
		t.Fatalf("reverse mapping lost original names: %#v", reverse)
	}
}

func TestAntigravityPreparedBodyRetainsGeminiSafetyAndThinkingSettings(t *testing.T) {
	credential := store.Credential{AuthType: "oauth", OAuthToken: &store.OAuthToken{Extra: map[string]any{"project_id": "cloud-project"}}}
	request := canonical.Request{
		Source:        canonical.ProtocolGemini,
		UpstreamModel: "gemini-3-pro",
		Raw: map[string]any{
			"contents":       []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello"}}}},
			"safetySettings": []any{map[string]any{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "OFF"}},
			"generationConfig": map[string]any{
				"thinkingConfig":  map[string]any{"thinkingBudget": float64(1024)},
				"maxOutputTokens": float64(2048),
			},
		},
	}

	body, _, err := antigravityPreparedBody(request, credential)
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := body["request"].(map[string]any)
	if inner["safetySettings"] == nil {
		t.Fatalf("safetySettings were unexpectedly stripped: %#v", inner)
	}
	generation, _ := inner["generationConfig"].(map[string]any)
	if generation["thinkingConfig"] == nil {
		t.Fatalf("thinkingConfig was unexpectedly stripped: %#v", generation)
	}
}

func TestAntigravityClampOutputTokensHandlesStringValues(t *testing.T) {
	request := map[string]any{"generationConfig": map[string]any{"maxOutputTokens": "90000"}}
	normalizeAntigravityRequest(request)
	generation := request["generationConfig"].(map[string]any)
	if got := generation["maxOutputTokens"]; got != antigravityMaxOutputTokens {
		t.Fatalf("maxOutputTokens=%#v, want %d", got, antigravityMaxOutputTokens)
	}
}

func TestAntigravityCleanSchemaFallsBackForNonJSONValues(t *testing.T) {
	cleaned := antigravityCleanSchema(map[string]any{"type": "object", "properties": map[string]any{"bad": math.NaN()}})
	schema, ok := cleaned.(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("cleaned=%#v", cleaned)
	}
}

func antigravitySchemaProperty(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	properties, _ := schema["properties"].(map[string]any)
	property, _ := properties[name].(map[string]any)
	if property == nil {
		t.Fatalf("missing property %q in %#v", name, schema)
	}
	return property
}

func antigravityUnsupportedSchemaKey(value any, unsupported map[string]struct{}) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, forbidden := unsupported[key]; forbidden {
				return key
			}
			if key == "properties" {
				if properties, ok := nested.(map[string]any); ok {
					for _, property := range properties {
						if forbidden := antigravityUnsupportedSchemaKey(property, unsupported); forbidden != "" {
							return forbidden
						}
					}
					continue
				}
			}
			if forbidden := antigravityUnsupportedSchemaKey(nested, unsupported); forbidden != "" {
				return forbidden
			}
		}
	case []any:
		for _, nested := range typed {
			if forbidden := antigravityUnsupportedSchemaKey(nested, unsupported); forbidden != "" {
				return forbidden
			}
		}
	}
	return ""
}
