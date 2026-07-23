package clitools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const grokBuiltinDefault = "grok-build"

var grokPrevDefaultRE = regexp.MustCompile(`(?m)^# (?:tproxy|9router)-prev-default = "([^"]*)"[ \t]*\r?\n?`)

func grokDir() string {
	return expandHome("~/.grok")
}

func grokConfigPath() string {
	return filepath.Join(grokDir(), "config.toml")
}

func grokBinPath() string {
	return filepath.Join(grokDir(), "bin", "grok")
}

func grokInstalled() bool {
	if commandInstalled("grok") {
		return true
	}
	if _, err := os.Stat(grokBinPath()); err == nil {
		return true
	}
	if _, err := os.Stat(grokConfigPath()); err == nil {
		return true
	}
	return false
}

func grokStatus() (StatusResult, error) {
	path := grokConfigPath()
	raw, err := readFile(path)
	if err != nil && !os.IsNotExist(err) {
		return StatusResult{}, err
	}
	configured := grokHasProxyConfig(string(raw))
	return StatusResult{
		Installed:    grokInstalled(),
		HasTproxy:    configured,
		Has9Router:   configured,
		SettingsPath: path,
		ConfigPath:   path,
	}, nil
}

func grokHasProxyConfig(toml string) bool {
	cfg := grokParseModelSection(toml)
	return cfg != nil && strings.TrimSpace(cfg["base_url"]) != ""
}

func grokIsModelSectionHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[model."+providerKey+"]") ||
		strings.HasPrefix(trimmed, "[model."+legacyProviderKey+"]")
}

func grokFindModelSection(toml string) (start, end int, ok bool) {
	lines := strings.Split(toml, "\n")
	offset := 0
	for i, line := range lines {
		if grokIsModelSectionHeader(line) {
			start = offset
			end = len(toml)
			sectionEnd := offset + len(line) + 1
			for j := i + 1; j < len(lines); j++ {
				trimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
					end = sectionEnd
					return start, end, true
				}
				sectionEnd += len(lines[j]) + 1
			}
			return start, end, true
		}
		offset += len(line) + 1
	}
	return 0, 0, false
}

func grokParseModelSection(toml string) map[string]string {
	start, end, ok := grokFindModelSection(toml)
	if !ok {
		return nil
	}
	body := toml[start:end]
	headerEnd := strings.Index(body, "\n")
	if headerEnd < 0 {
		return map[string]string{}
	}
	body = body[headerEnd+1:]
	fields := []string{"model", "base_url", "name", "api_key", "api_backend", "description"}
	out := make(map[string]string, len(fields))
	for _, key := range fields {
		prefix := key + ` = "`
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, `"`) {
				out[key] = trimmed[len(prefix) : len(trimmed)-1]
				break
			}
		}
	}
	return out
}

func grokFindModelsSection(toml string) (start, end int, bodyStart int, ok bool) {
	lines := strings.Split(toml, "\n")
	offset := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "[models]" {
			start = offset
			bodyStart = offset + len(line) + 1
			end = len(toml)
			sectionEnd := bodyStart
			for j := i + 1; j < len(lines); j++ {
				trimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
					end = sectionEnd
					return start, end, bodyStart, true
				}
				sectionEnd += len(lines[j]) + 1
			}
			return start, end, bodyStart, true
		}
		offset += len(line) + 1
	}
	return 0, 0, 0, false
}

func grokParseModelsDefault(toml string) string {
	_, end, bodyStart, ok := grokFindModelsSection(toml)
	if !ok {
		return ""
	}
	body := strings.TrimSpace(toml[bodyStart:end])
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `default = "`) && strings.HasSuffix(trimmed, `"`) {
			return trimmed[len(`default = "`): len(trimmed)-1]
		}
	}
	return ""
}

func grokBuildModelSection(model, baseURL, apiKey string) string {
	lines := []string{
		fmt.Sprintf("[model.%s]", providerKey),
		fmt.Sprintf(`model = "%s"`, model),
		fmt.Sprintf(`base_url = "%s"`, baseURL),
		`name = "TProxy"`,
		`description = "Routed via TProxy gateway"`,
		`api_backend = "chat_completions"`,
	}
	if strings.TrimSpace(apiKey) != "" {
		lines = append(lines, fmt.Sprintf(`api_key = "%s"`, apiKey))
	}
	return strings.Join(lines, "\n") + "\n"
}

func grokUpsertModelSection(toml, section string) string {
	start, end, ok := grokFindModelSection(toml)
	if ok {
		return toml[:start] + section + toml[end:]
	}
	if toml != "" && !strings.HasSuffix(toml, "\n") {
		toml += "\n"
	}
	if toml != "" {
		toml += "\n"
	}
	return toml + section
}

func grokRemoveModelSection(toml string) string {
	start, end, ok := grokFindModelSection(toml)
	if !ok {
		return toml
	}
	next := toml[:start] + toml[end:]
	return regexp.MustCompile(`\n{3,}`).ReplaceAllString(next, "\n\n")
}

func grokSetModelsDefault(toml, value string) string {
	_, end, bodyStart, ok := grokFindModelsSection(toml)
	if ok {
		body := toml[bodyStart:end]
		lines := strings.Split(body, "\n")
		found := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, `default = "`) {
				lines[i] = fmt.Sprintf(`default = "%s"`, value)
				found = true
				break
			}
		}
		if !found {
			lines = append([]string{fmt.Sprintf(`default = "%s"`, value)}, lines...)
		}
		newBody := strings.Join(lines, "\n")
		if end < len(toml) && toml[end-1] != '\n' && newBody != "" {
			newBody += "\n"
		}
		return toml[:bodyStart] + newBody + toml[end:]
	}
	block := fmt.Sprintf("[models]\ndefault = \"%s\"\n\n", value)
	if toml == "" {
		return block
	}
	return block + toml
}

func grokRememberPrevDefault(toml string) string {
	if grokPrevDefaultRE.MatchString(toml) {
		return toml
	}
	current := grokParseModelsDefault(toml)
	if current == "" || current == providerKey || current == legacyProviderKey {
		return toml
	}
	marker := fmt.Sprintf("# tproxy-prev-default = \"%s\"\n", current)
	start, end, ok := grokFindModelSection(toml)
	if ok {
		return toml[:start] + marker + toml[start:end] + toml[end:]
	}
	if toml != "" && !strings.HasSuffix(toml, "\n") {
		toml += "\n"
	}
	return toml + marker
}

func grokClearModelsDefaultIfOurs(toml string) string {
	prevMatch := grokPrevDefaultRE.FindStringSubmatch(toml)
	restoreTo := grokBuiltinDefault
	if len(prevMatch) > 1 && strings.TrimSpace(prevMatch[1]) != "" {
		restoreTo = prevMatch[1]
	}
	next := grokPrevDefaultRE.ReplaceAllString(toml, "")
	current := grokParseModelsDefault(next)
	if current == providerKey || current == legacyProviderKey {
		next = grokSetModelsDefault(next, restoreTo)
	}
	return next
}

func grokApply(req ApplyRequest) error {
	models := modelsFromRequest(req)
	if len(models) == 0 || req.BaseURL == "" || req.APIKey == "" {
		return fmt.Errorf("baseUrl, apiKey and model are required")
	}

	existing, err := readFile(grokConfigPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	toml := string(existing)
	toml = grokRememberPrevDefault(toml)
	toml = grokUpsertModelSection(toml, grokBuildModelSection(models[0], normalizeBaseURL(req.BaseURL, true), req.APIKey))
	toml = grokSetModelsDefault(toml, providerKey)
	return writeFile(grokConfigPath(), []byte(toml))
}

func grokReset() error {
	existing, err := readFile(grokConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	toml := grokRemoveModelSection(string(existing))
	toml = grokClearModelsDefaultIfOurs(toml)
	toml = strings.TrimSpace(toml)
	if toml == "" {
		return os.Remove(grokConfigPath())
	}
	if !strings.HasSuffix(toml, "\n") {
		toml += "\n"
	}
	return writeFile(grokConfigPath(), []byte(toml))
}
