package clitools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func claudeSettingsPath() string { return expandHome("~/.claude/settings.json") }

func claudeStatus() (StatusResult, error) {
	path := claudeSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	env := asMap(settings["env"])
	endpoint := asString(env["ANTHROPIC_BASE_URL"])
	configured := endpointLooksLikeProxy(endpoint)
	return statusFromInstalled("claude", path, configured).with(endpoint, asString(env["ANTHROPIC_MODEL"])), nil
}

func claudeApply(req ApplyRequest) error {
	path := claudeSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return err
	}
	env := asMap(settings["env"])
	if req.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = normalizeBaseURL(req.BaseURL, true)
	}
	if req.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = req.APIKey
	}
	primary := normalizeClaudePrimaryModel(strings.TrimSpace(req.Model))
	applyClaudeCodeClientEnv(env, primary, normalizeClaudePrimaryModel(subagentOf(req, primary)))
	// req.Env is applied last so the dashboard can override individual tier slots
	// (ANTHROPIC_DEFAULT_SONNET_MODEL, …) on top of the generated defaults.
	for key, value := range req.Env {
		env[key] = value
	}
	settings["env"] = env
	settings["hasCompletedOnboarding"] = true
	return writeJSONFile(path, settings)
}

func normalizeClaudePrimaryModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "fable", "opus", "sonnet", "haiku":
		return strings.ToLower(strings.TrimSpace(model))
	default:
		return "fable"
	}
}

func applyClaudeCodeClientEnv(env map[string]any, primary string, subagent string) {
	if subagent == "" {
		subagent = primary
	}
	env["ANTHROPIC_MODEL"] = primary
	env["ANTHROPIC_DEFAULT_FABLE_MODEL"] = "fable"
	env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "opus"
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "sonnet"
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "haiku"
	env["CLAUDE_CODE_SUBAGENT_MODEL"] = subagent
	env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "1048576"
	env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = "1048576"
}

func claudeReset() error {
	path := claudeSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return err
	}
	env := asMap(settings["env"])
	for _, key := range []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_MAX_CONTEXT_TOKENS",
	} {
		delete(env, key)
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	return writeJSONFile(path, settings)
}

func codexConfigPath() string { return expandHome("~/.codex/config.toml") }
func codexAuthPath() string   { return expandHome("~/.codex/auth.json") }

func codexStatus() (StatusResult, error) {
	path := codexConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	configured := strings.Contains(string(raw), providerKey) || strings.Contains(string(raw), legacyProviderKey)
	parsed := map[string]any{}
	_ = toml.Unmarshal(raw, &parsed)
	endpoint := ""
	if providers := asMap(parsed["model_providers"]); providers != nil {
		entry := asMap(providers[providerKey])
		if len(entry) == 0 {
			entry = asMap(providers[legacyProviderKey])
		}
		endpoint = asString(entry["base_url"])
	}
	return statusFromInstalled("codex", path, configured).with(endpoint, asString(parsed["model"])), nil
}

func codexApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	model := models[0]
	path := codexConfigPath()
	existing := ""
	if raw, err := readFile(path); err == nil {
		existing = string(raw)
	}
	updates := map[string]any{
		"model":          model,
		"model_provider": providerKey,
		"model_providers": map[string]any{
			providerKey: map[string]any{
				"name":                 "TProxy",
				"base_url":             normalizeBaseURL(req.BaseURL, true),
				"wire_api":             "responses",
				"requires_openai_auth": true,
			},
		},
		"agents": map[string]any{
			"subagent": map[string]any{"model": subagentOf(req, model)},
		},
	}
	merged, err := mergeTomlMap(existing, updates)
	if err != nil {
		return err
	}
	if err := writeFile(path, []byte(merged)); err != nil {
		return err
	}
	auth, _ := readJSONFile(codexAuthPath())
	auth["OPENAI_API_KEY"] = req.APIKey
	auth["auth_mode"] = "apikey"
	return writeJSONFile(codexAuthPath(), auth)
}

func codexReset() error {
	path := codexConfigPath()
	existing := ""
	if raw, err := readFile(path); err == nil {
		existing = string(raw)
	}
	parsed := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := toml.Unmarshal([]byte(existing), &parsed); err != nil {
			return err
		}
	}
	if asString(parsed["model_provider"]) == providerKey || asString(parsed["model_provider"]) == legacyProviderKey {
		delete(parsed, "model")
		delete(parsed, "model_provider")
	}
	deleteNestedMap(parsed, "model_providers."+providerKey)
	deleteNestedMap(parsed, "model_providers."+legacyProviderKey)
	deleteNestedMap(parsed, "agents.subagent")
	merged, err := toml.Marshal(parsed)
	if err != nil {
		return err
	}
	if err := writeFile(path, merged); err != nil {
		return err
	}
	auth, _ := readJSONFile(codexAuthPath())
	delete(auth, "OPENAI_API_KEY")
	delete(auth, "auth_mode")
	if len(auth) == 0 {
		return os.Remove(codexAuthPath())
	}
	return writeJSONFile(codexAuthPath(), auth)
}

func openclawSettingsPath() string { return expandHome("~/.openclaw/openclaw.json") }

func openclawStatus() (StatusResult, error) {
	path := openclawSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	models := asMap(settings["models"])
	providers := asMap(models["providers"])
	entry, hasTproxy := providers[providerKey]
	if !hasTproxy {
		entry = providers[legacyProviderKey]
	}
	_, hasLegacy := providers[legacyProviderKey]
	provider := asMap(entry)
	primary := asString(asMap(asMap(asMap(settings["agents"])["defaults"])["model"])["primary"])
	return statusFromInstalled("openclaw", path, hasTproxy || hasLegacy).
		with(asString(provider["baseUrl"]), strings.TrimPrefix(primary, providerKey+"/")), nil
}

func openclawApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" {
		return fmt.Errorf("baseUrl and model are required")
	}
	model := models[0]
	path := openclawSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return err
	}
	if settings["models"] == nil {
		settings["models"] = map[string]any{}
	}
	modelRoot := asMap(settings["models"])
	if modelRoot["providers"] == nil {
		modelRoot["providers"] = map[string]any{}
	}
	providers := asMap(modelRoot["providers"])
	modelEntries := make([]any, 0, len(models))
	for _, item := range models {
		modelEntries = append(modelEntries, map[string]any{"id": item, "name": filepath.Base(item)})
	}
	providers[providerKey] = map[string]any{
		"baseUrl": normalizeBaseURL(req.BaseURL, true),
		"apiKey":  req.APIKey,
		"api":     "openai-completions",
		"models":  modelEntries,
	}
	modelRoot["providers"] = providers
	settings["models"] = modelRoot
	if settings["agents"] == nil {
		settings["agents"] = map[string]any{}
	}
	agents := asMap(settings["agents"])
	if agents["defaults"] == nil {
		agents["defaults"] = map[string]any{}
	}
	defaults := asMap(agents["defaults"])
	defaults["model"] = map[string]any{"primary": providerKey + "/" + model}
	agents["defaults"] = defaults
	settings["agents"] = agents
	return writeJSONFile(path, settings)
}

func openclawReset() error {
	path := openclawSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return err
	}
	modelRoot := asMap(settings["models"])
	providers := asMap(modelRoot["providers"])
	delete(providers, providerKey)
	delete(providers, legacyProviderKey)
	modelRoot["providers"] = providers
	settings["models"] = modelRoot
	return writeJSONFile(path, settings)
}

func opencodeConfigPath() string { return expandHome("~/.config/opencode/opencode.json") }

func opencodeStatus() (StatusResult, error) {
	path := opencodeConfigPath()
	config, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	providers := asMap(config["provider"])
	entry, hasTproxy := providers[providerKey]
	if !hasTproxy {
		entry = providers[legacyProviderKey]
	}
	_, hasLegacy := providers[legacyProviderKey]
	endpoint := asString(asMap(asMap(entry)["options"])["baseURL"])
	active := asString(config["model"])
	if idx := strings.Index(active, "/"); idx >= 0 {
		active = active[idx+1:]
	}
	return statusFromInstalled("opencode", path, hasTproxy || hasLegacy).with(endpoint, active), nil
}

func opencodeApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" {
		return fmt.Errorf("baseUrl and model are required")
	}
	path := opencodeConfigPath()
	config, err := readJSONFile(path)
	if err != nil {
		return err
	}
	if config["provider"] == nil {
		config["provider"] = map[string]any{}
	}
	providers := asMap(config["provider"])
	// Preserve models added by earlier applies instead of replacing the whole map,
	// so re-applying with one model does not drop the others.
	existing := asMap(providers[providerKey])
	modelMap := asMap(existing["models"])
	for _, model := range models {
		modelMap[model] = map[string]any{
			"name": model,
			"modalities": map[string]any{
				"input":  []any{"text", "image"},
				"output": []any{"text"},
			},
		}
	}
	options := asMap(existing["options"])
	options["baseURL"] = normalizeBaseURL(req.BaseURL, true)
	options["apiKey"] = req.APIKey
	existing["npm"] = "@ai-sdk/openai-compatible"
	existing["options"] = options
	existing["models"] = modelMap
	providers[providerKey] = existing
	config["provider"] = providers
	config["model"] = providerKey + "/" + models[0]

	agents := asMap(config["agent"])
	agents["explorer"] = map[string]any{
		"description": "Fast explorer subagent for codebase exploration",
		"mode":        "subagent",
		"model":       providerKey + "/" + subagentOf(req, models[0]),
	}
	config["agent"] = agents
	return writeJSONFile(path, config)
}

func opencodeReset() error {
	path := opencodeConfigPath()
	config, err := readJSONFile(path)
	if err != nil {
		return err
	}
	providers := asMap(config["provider"])
	delete(providers, providerKey)
	delete(providers, legacyProviderKey)
	config["provider"] = providers
	if strings.HasPrefix(asString(config["model"]), providerKey+"/") || strings.HasPrefix(asString(config["model"]), legacyProviderKey+"/") {
		delete(config, "model")
	}
	if agents := asMap(config["agent"]); len(agents) > 0 {
		explorerModel := asString(asMap(agents["explorer"])["model"])
		if strings.HasPrefix(explorerModel, providerKey+"/") || strings.HasPrefix(explorerModel, legacyProviderKey+"/") {
			delete(agents, "explorer")
		}
		if len(agents) == 0 {
			delete(config, "agent")
		} else {
			config["agent"] = agents
		}
	}
	return writeJSONFile(path, config)
}

func clineGlobalStatePath() string { return expandHome("~/.cline/data/globalState.json") }
func clineSecretsPath() string     { return expandHome("~/.cline/data/secrets.json") }

func clineStatus() (StatusResult, error) {
	path := clineGlobalStatePath()
	state, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	endpoint := asString(state["openAiBaseUrl"])
	configured := endpointLooksLikeProxy(endpoint)
	return statusFromInstalled("cline", path, configured).with(endpoint, asString(state["openAiModelId"])), nil
}

func clineApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	state, err := readJSONFile(clineGlobalStatePath())
	if err != nil {
		return err
	}
	baseURL := normalizeBaseURL(req.BaseURL, false)
	state["actModeApiProvider"] = "openai"
	state["planModeApiProvider"] = "openai"
	state["openAiBaseUrl"] = baseURL
	state["openAiModelId"] = models[0]
	state["planModeOpenAiModelId"] = models[0]
	if err := writeJSONFile(clineGlobalStatePath(), state); err != nil {
		return err
	}
	secrets, err := readJSONFile(clineSecretsPath())
	if err != nil {
		return err
	}
	secrets["openAiApiKey"] = req.APIKey
	return writeJSONFile(clineSecretsPath(), secrets)
}

func clineReset() error {
	state, err := readJSONFile(clineGlobalStatePath())
	if err != nil {
		return err
	}
	if asString(state["actModeApiProvider"]) == "openai" {
		delete(state, "openAiBaseUrl")
		delete(state, "openAiModelId")
		delete(state, "planModeOpenAiModelId")
		state["actModeApiProvider"] = "cline"
		state["planModeApiProvider"] = "cline"
	}
	if err := writeJSONFile(clineGlobalStatePath(), state); err != nil {
		return err
	}
	secrets, err := readJSONFile(clineSecretsPath())
	if err != nil {
		return err
	}
	delete(secrets, "openAiApiKey")
	return writeJSONFile(clineSecretsPath(), secrets)
}

func droidSettingsPath() string { return expandHome("~/.factory/settings.json") }

func droidStatus() (StatusResult, error) {
	path := droidSettingsPath()
	settings, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	configured := false
	endpoint, model := "", ""
	for _, item := range asSlice(settings["customModels"]) {
		entry := asMap(item)
		id := asString(entry["id"])
		if strings.HasPrefix(id, "custom:tproxy") || strings.HasPrefix(id, "custom:9Router") {
			configured = true
			endpoint = asString(entry["baseUrl"])
			model = asString(entry["model"])
			break
		}
	}
	return statusFromInstalled("droid", path, configured).with(endpoint, model), nil
}

func droidApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" {
		return fmt.Errorf("baseUrl and model are required")
	}
	settings, err := readJSONFile(droidSettingsPath())
	if err != nil {
		return err
	}
	custom := make([]any, 0)
	for _, item := range asSlice(settings["customModels"]) {
		entry := asMap(item)
		id := asString(entry["id"])
		if strings.HasPrefix(id, "custom:tproxy") || strings.HasPrefix(id, "custom:9Router") {
			continue
		}
		custom = append(custom, item)
	}
	baseURL := normalizeBaseURL(req.BaseURL, true)
	for i, model := range models {
		custom = append([]any{map[string]any{
			"model":           model,
			"id":              fmt.Sprintf("custom:tproxy-%d", i),
			"index":           i,
			"baseUrl":         baseURL,
			"apiKey":          req.APIKey,
			"displayName":     model,
			"maxOutputTokens": 131072,
			"noImageSupport":  false,
			"provider":        "openai",
		}}, custom...)
	}
	settings["customModels"] = custom
	return writeJSONFile(droidSettingsPath(), settings)
}

func droidReset() error {
	settings, err := readJSONFile(droidSettingsPath())
	if err != nil {
		return err
	}
	custom := make([]any, 0)
	for _, item := range asSlice(settings["customModels"]) {
		entry := asMap(item)
		id := asString(entry["id"])
		if strings.HasPrefix(id, "custom:tproxy") || strings.HasPrefix(id, "custom:9Router") {
			continue
		}
		custom = append(custom, item)
	}
	if len(custom) == 0 {
		delete(settings, "customModels")
	} else {
		settings["customModels"] = custom
	}
	return writeJSONFile(droidSettingsPath(), settings)
}

func kiloAuthPath() string { return expandHome("~/.local/share/kilo/auth.json") }

func kiloStatus() (StatusResult, error) {
	path := kiloAuthPath()
	auth, err := readJSONFile(path)
	if err != nil {
		return StatusResult{}, err
	}
	entry := asMap(auth["openai-compatible"])
	if len(entry) == 0 {
		entry = asMap(auth[legacyProviderKey])
	}
	endpoint := asString(entry["baseUrl"])
	if endpoint == "" {
		endpoint = asString(entry["baseURL"])
	}
	configured := endpointLooksLikeProxy(endpoint)
	return statusFromInstalled("kilo", path, configured).with(endpoint, asString(entry["model"])), nil
}

func kiloApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	auth, err := readJSONFile(kiloAuthPath())
	if err != nil {
		return err
	}
	auth["openai-compatible"] = map[string]any{
		"type":    "api-key",
		"apiKey":  req.APIKey,
		"baseUrl": normalizeBaseURL(req.BaseURL, true),
		"model":   models[0],
	}
	return writeJSONFile(kiloAuthPath(), auth)
}

func kiloReset() error {
	auth, err := readJSONFile(kiloAuthPath())
	if err != nil {
		return err
	}
	delete(auth, "openai-compatible")
	delete(auth, legacyProviderKey)
	return writeJSONFile(kiloAuthPath(), auth)
}

func deepseekConfigPath() string { return expandHome("~/.deepseek/config.toml") }

func deepseekStatus() (StatusResult, error) {
	path := deepseekConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	configured := strings.Contains(string(raw), `provider = "openai"`)
	parsed := map[string]any{}
	_ = toml.Unmarshal(raw, &parsed)
	openai := asMap(asMap(parsed["providers"])["openai"])
	return statusFromInstalled("deepseek", path, configured).
		with(asString(openai["base_url"]), asString(openai["model"])), nil
}

func deepseekApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	content := fmt.Sprintf(`provider = "openai"

[providers.openai]
base_url = "%s"
api_key = "%s"
model = "%s"
`, normalizeBaseURL(req.BaseURL, true), req.APIKey, models[0])
	return writeFile(deepseekConfigPath(), []byte(content))
}

func deepseekReset() error {
	return writeFile(deepseekConfigPath(), []byte(`provider = "deepseek"`+"\n"))
}

func copilotConfigPath() string {
	home := homeDir()
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Code", "User", "chatLanguageModels.json")
	default:
		return expandHome("~/.config/Code/User/chatLanguageModels.json")
	}
}

func copilotStatus() (StatusResult, error) {
	path := copilotConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	configured := strings.Contains(string(raw), `"name": "TProxy"`) || strings.Contains(string(raw), `"name": "9Router"`)
	endpoint, model := "", ""
	var parsed []any
	if stripped := trailingCommaRE.ReplaceAllString(string(raw), "$1"); stripped != "" {
		_ = json.Unmarshal([]byte(stripped), &parsed)
	}
	for _, item := range parsed {
		entry := asMap(item)
		if name := asString(entry["name"]); name != "TProxy" && name != "9Router" {
			continue
		}
		if first := asSlice(entry["models"]); len(first) > 0 {
			modelEntry := asMap(first[0])
			endpoint = asString(modelEntry["url"])
			model = asString(modelEntry["id"])
		}
		break
	}
	return statusFromInstalled("", path, configured).with(endpoint, model), nil
}

func copilotApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" {
		return fmt.Errorf("baseUrl and model are required")
	}
	path := copilotConfigPath()
	var config []any
	if raw, err := readFile(path); err == nil {
		stripped := trailingCommaRE.ReplaceAllString(string(raw), "$1")
		_ = json.Unmarshal([]byte(stripped), &config)
	}
	filtered := make([]any, 0, len(config))
	for _, item := range config {
		entry := asMap(item)
		name := asString(entry["name"])
		if name == "TProxy" || name == "9Router" {
			continue
		}
		filtered = append(filtered, item)
	}
	modelEntries := make([]any, 0, len(models))
	baseURL := normalizeBaseURL(req.BaseURL, true)
	for _, model := range models {
		modelEntries = append(modelEntries, map[string]any{
			"id":   model,
			"name": model,
			"url":  baseURL,
		})
	}
	filtered = append(filtered, map[string]any{
		"name":   "TProxy",
		"models": modelEntries,
		"apiKey": req.APIKey,
	})
	return writeJSONFile(path, filtered)
}

func copilotReset() error {
	path := copilotConfigPath()
	var config []any
	if raw, err := readFile(path); err == nil {
		stripped := trailingCommaRE.ReplaceAllString(string(raw), "$1")
		_ = json.Unmarshal([]byte(stripped), &config)
	}
	filtered := make([]any, 0, len(config))
	for _, item := range config {
		entry := asMap(item)
		name := asString(entry["name"])
		if name == "TProxy" || name == "9Router" {
			continue
		}
		filtered = append(filtered, item)
	}
	return writeJSONFile(path, filtered)
}

var hermesModelBlockRE = regexp.MustCompile(`(?m)^model:[ \t]*\r?\n(?:(?:[ \t]+.*\r?\n?|[ \t]*\r?\n)*)`)

func hermesConfigPath() string { return expandHome("~/.hermes/config.yaml") }
func hermesEnvPath() string    { return expandHome("~/.hermes/.env") }

func hermesStatus() (StatusResult, error) {
	path := hermesConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	endpoint := firstYAMLValue(string(raw), "base_url")
	configured := strings.Contains(string(raw), `provider: "custom"`) && endpointLooksLikeProxy(endpoint)
	return statusFromInstalled("hermes", path, configured).
		with(endpoint, firstYAMLValue(string(raw), "default")), nil
}

// firstYAMLValue pulls a scalar out of Hermes' small config.yaml without pulling in
// a YAML dependency — the file only ever holds a flat model block.
func firstYAMLValue(raw, key string) string {
	re, err := regexp.Compile(fmt.Sprintf(`(?m)^[ \t]*%s:[ \t]*"?([^"\r\n]+?)"?[ \t]*$`, regexp.QuoteMeta(key)))
	if err != nil {
		return ""
	}
	match := re.FindStringSubmatch(raw)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func hermesApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	baseURL := normalizeBaseURL(req.BaseURL, true)
	block := fmt.Sprintf("model:\n  default: \"%s\"\n  provider: \"custom\"\n  base_url: \"%s\"\n", models[0], baseURL)
	raw := ""
	if existing, err := readFile(hermesConfigPath()); err == nil {
		raw = string(existing)
	}
	if hermesModelBlockRE.MatchString(raw) {
		raw = hermesModelBlockRE.ReplaceAllString(raw, block)
	} else if strings.TrimSpace(raw) == "" {
		raw = block
	} else {
		raw = block + "\n" + raw
	}
	if err := writeFile(hermesConfigPath(), []byte(raw)); err != nil {
		return err
	}
	env := ""
	if existing, err := readFile(hermesEnvPath()); err == nil {
		env = string(existing)
	}
	env = upsertEnvLine(env, "OPENAI_API_KEY", req.APIKey)
	return writeFile(hermesEnvPath(), []byte(env))
}

func hermesReset() error {
	raw := ""
	if existing, err := readFile(hermesConfigPath()); err == nil {
		raw = hermesModelBlockRE.ReplaceAllString(string(existing), "")
	}
	if err := writeFile(hermesConfigPath(), []byte(strings.TrimLeft(raw, "\n"))); err != nil {
		return err
	}
	env := ""
	if existing, err := readFile(hermesEnvPath()); err == nil {
		env = removeEnvLine(string(existing), "OPENAI_API_KEY")
	}
	return writeFile(hermesEnvPath(), []byte(env))
}

func jcodeConfigPath() string { return expandHome("~/.jcode/config.toml") }

func jcodeStatus() (StatusResult, error) {
	path := jcodeConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	configured := strings.Contains(string(raw), providerKey) || strings.Contains(string(raw), legacyProviderKey)
	parsed := map[string]any{}
	_ = toml.Unmarshal(raw, &parsed)
	entry := asMap(asMap(parsed["providers"])[providerKey])
	if len(entry) == 0 {
		entry = asMap(asMap(parsed["providers"])[legacyProviderKey])
	}
	return statusFromInstalled("jcode", path, configured).
		with(asString(entry["base_url"]), asString(entry["model"])), nil
}

func jcodeApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}
	existing := ""
	if raw, err := readFile(jcodeConfigPath()); err == nil {
		existing = string(raw)
	}
	updates := map[string]any{
		"providers": map[string]any{
			providerKey: map[string]any{
				"base_url": normalizeBaseURL(req.BaseURL, true),
				"api_key":  req.APIKey,
				"model":    models[0],
			},
		},
	}
	merged, err := mergeTomlMap(existing, updates)
	if err != nil {
		return err
	}
	return writeFile(jcodeConfigPath(), []byte(merged))
}

func jcodeReset() error {
	existing := ""
	if raw, err := readFile(jcodeConfigPath()); err == nil {
		existing = string(raw)
	}
	parsed := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := toml.Unmarshal([]byte(existing), &parsed); err != nil {
			return err
		}
	}
	deleteNestedMap(parsed, "providers."+providerKey)
	deleteNestedMap(parsed, "providers."+legacyProviderKey)
	merged, err := toml.Marshal(parsed)
	if err != nil {
		return err
	}
	return writeFile(jcodeConfigPath(), merged)
}

func upsertEnvLine(envText, key, value string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)
	line := key + "=" + value
	if re.MatchString(envText) {
		return re.ReplaceAllString(envText, line)
	}
	if envText != "" && !strings.HasSuffix(envText, "\n") {
		envText += "\n"
	}
	return envText + line + "\n"
}

func removeEnvLine(envText, key string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*\r?\n?`)
	return re.ReplaceAllString(envText, "")
}
