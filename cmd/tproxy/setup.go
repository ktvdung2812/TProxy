package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// `tproxy setup` is the terminal equivalent of the dashboard's CLI Tools page:
// pick a tool, pick an API key and a model, apply. It drives the same admin API
// the dashboard uses, so behaviour cannot drift between the two.

const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiCyan  = "\x1b[36m"
	ansiBold  = "\x1b[1m"
)

// setupToolLabels keeps the TUI list readable and ordered; ids match the registry
// in internal/clitools.
var setupToolLabels = []struct {
	ID   string
	Name string
}{
	{"claude", "Claude Code"},
	{"codex", "Codex CLI"},
	{"opencode", "OpenCode"},
	{"openclaw", "Open Claw"},
	{"droid", "Factory Droid"},
	{"cline", "Cline"},
	{"kilo", "Kilo Code"},
	{"cowork", "Claude Cowork"},
	{"copilot", "GitHub Copilot (BYOK)"},
	{"hermes", "Hermes Agent"},
	{"deepseek-tui", "DeepSeek TUI"},
	{"grok-build", "Grok Build"},
	{"jcode", "jcode"},
}

type setupClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

type setupToolStatus struct {
	Installed  bool   `json:"installed"`
	HasTproxy  bool   `json:"has_tproxy"`
	Has9Router bool   `json:"has_9router"`
	ConfigPath string `json:"config_path"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	Message    string `json:"message"`
}

type setupSnapshot struct {
	Models []struct {
		ID          string `json:"ID"`
		DisplayName string `json:"DisplayName"`
		Enabled     *bool  `json:"Enabled"`
	} `json:"models"`
	Combos []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Enabled     *bool  `json:"enabled"`
	} `json:"combos"`
	APIKeys []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled"`
	} `json:"api_keys"`
}

func maybeRunSetup(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "setup" {
		return false, nil
	}
	return true, runSetup(args[1:])
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	baseURL := fs.String("url", defaultSetupURL(), "tproxy base URL")
	secret := fs.String("secret", os.Getenv("TPROXY_ADMIN_SECRET"), "dashboard admin secret")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := &setupClient{
		baseURL: strings.TrimRight(strings.TrimSpace(*baseURL), "/"),
		secret:  strings.TrimSpace(*secret),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
	reader := bufio.NewReader(os.Stdin)

	if client.secret == "" {
		fmt.Printf("Admin secret (leave blank if the dashboard has no password): ")
		line, _ := reader.ReadString('\n')
		client.secret = strings.TrimSpace(line)
	}

	snapshot, err := client.snapshot()
	if err != nil {
		return fmt.Errorf("cannot reach tproxy at %s: %w", client.baseURL, err)
	}

	for {
		statuses, err := client.statuses()
		if err != nil {
			return err
		}
		printToolMenu(client.baseURL, statuses)

		fmt.Printf("\nSelect a tool (number), or q to quit: ")
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil
		}
		choice := strings.TrimSpace(line)
		if choice == "" {
			continue
		}
		if choice == "q" || choice == "Q" {
			return nil
		}
		index, convErr := strconv.Atoi(choice)
		if convErr != nil || index < 1 || index > len(setupToolLabels) {
			fmt.Println("Invalid selection.")
			continue
		}
		tool := setupToolLabels[index-1]
		if err := client.toolMenu(reader, tool.ID, tool.Name, snapshot); err != nil {
			fmt.Printf("%s%v%s\n", ansiRed, err, ansiReset)
		}
	}
}

func defaultSetupURL() string {
	if raw := strings.TrimSpace(os.Getenv("TPROXY_URL")); raw != "" {
		return raw
	}
	return "http://127.0.0.1:28120"
}

func printToolMenu(baseURL string, statuses map[string]setupToolStatus) {
	fmt.Printf("\n%s🔧 tproxy — CLI Tools%s\n", ansiBold, ansiReset)
	fmt.Printf("%sEndpoint: %s%s\n\n", ansiDim, baseURL, ansiReset)
	for index, tool := range setupToolLabels {
		status := statuses[tool.ID]
		fmt.Printf("  %2d) %-24s %s\n", index+1, tool.Name, setupStatusLabel(status))
	}
}

func setupStatusLabel(status setupToolStatus) string {
	if !status.Installed {
		return ansiDim + "not installed" + ansiReset
	}
	if status.HasTproxy || status.Has9Router {
		label := ansiGreen + "✓ configured" + ansiReset
		if status.Model != "" {
			label += fmt.Sprintf(" %s(%s)%s", ansiDim, status.Model, ansiReset)
		}
		return label
	}
	return ansiRed + "✗ not configured" + ansiReset
}

func (c *setupClient) toolMenu(reader *bufio.Reader, toolID, toolName string, snapshot setupSnapshot) error {
	for {
		status, err := c.status(toolID)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s%s%s\n", ansiBold, toolName, ansiReset)
		fmt.Printf("  Status:   %s\n", setupStatusLabel(status))
		if status.ConfigPath != "" {
			fmt.Printf("  Config:   %s%s%s\n", ansiDim, status.ConfigPath, ansiReset)
		}
		if status.Endpoint != "" {
			fmt.Printf("  Endpoint: %s%s%s\n", ansiCyan, status.Endpoint, ansiReset)
		}
		fmt.Println("\n  1) Quick Setup")
		fmt.Println("  2) Reset to default")
		fmt.Println("  b) Back")
		fmt.Printf("\nChoice: ")

		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil
		}
		switch strings.TrimSpace(line) {
		case "1":
			if err := c.quickSetup(reader, toolID, snapshot); err != nil {
				fmt.Printf("%sFailed: %v%s\n", ansiRed, err, ansiReset)
			} else {
				fmt.Printf("%s✓ Applied.%s\n", ansiGreen, ansiReset)
			}
		case "2":
			if err := c.reset(toolID); err != nil {
				fmt.Printf("%sFailed: %v%s\n", ansiRed, err, ansiReset)
			} else {
				fmt.Printf("%s✓ Reset.%s\n", ansiGreen, ansiReset)
			}
		case "b", "B", "":
			return nil
		default:
			fmt.Println("Invalid selection.")
		}
	}
}

func (c *setupClient) quickSetup(reader *bufio.Reader, toolID string, snapshot setupSnapshot) error {
	keyID, err := pickAPIKey(reader, snapshot)
	if err != nil {
		return err
	}
	secrets, err := c.apiKeySecrets()
	if err != nil {
		return err
	}
	apiKey := secrets[keyID]
	if apiKey == "" {
		fmt.Printf("Secret for %s is not resolvable on this host. Paste the key: ", keyID)
		line, _ := reader.ReadString('\n')
		apiKey = strings.TrimSpace(line)
	}
	if apiKey == "" {
		return fmt.Errorf("an API key is required")
	}

	models := setupModelChoices(snapshot)
	if len(models) == 0 {
		return fmt.Errorf("no models or combos configured yet — add one in the dashboard first")
	}
	model, err := pickFromList(reader, "Select a model", models)
	if err != nil {
		return err
	}

	extra := []string{model}
	for {
		fmt.Printf("Add another model? [y/N]: ")
		line, _ := reader.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(line), "y") {
			break
		}
		next, pickErr := pickFromList(reader, "Select a model", models)
		if pickErr != nil {
			break
		}
		if !contains(extra, next) {
			extra = append(extra, next)
		}
	}

	subagent := ""
	fmt.Printf("Use a different subagent model? [y/N]: ")
	if line, _ := reader.ReadString('\n'); strings.EqualFold(strings.TrimSpace(line), "y") {
		picked, pickErr := pickFromList(reader, "Select a subagent model", models)
		if pickErr == nil {
			subagent = picked
		}
	}

	payload := map[string]any{
		"baseUrl": c.baseURL,
		"apiKey":  apiKey,
		"model":   model,
	}
	if len(extra) > 1 {
		payload["models"] = extra
	}
	if subagent != "" {
		payload["subagentModel"] = subagent
	}
	return c.apply(toolID, payload)
}

func pickAPIKey(reader *bufio.Reader, snapshot setupSnapshot) (string, error) {
	options := make([]string, 0, len(snapshot.APIKeys))
	for _, key := range snapshot.APIKeys {
		if key.Enabled != nil && !*key.Enabled {
			continue
		}
		options = append(options, key.ID)
	}
	if len(options) == 0 {
		return "", fmt.Errorf("no API keys yet — create one in the dashboard first")
	}
	if len(options) == 1 {
		return options[0], nil
	}
	return pickFromList(reader, "Select an API key", options)
}

func setupModelChoices(snapshot setupSnapshot) []string {
	seen := map[string]bool{}
	options := make([]string, 0, len(snapshot.Models)+len(snapshot.Combos))
	for _, model := range snapshot.Models {
		if model.Enabled != nil && !*model.Enabled {
			continue
		}
		if model.ID != "" && !seen[model.ID] {
			seen[model.ID] = true
			options = append(options, model.ID)
		}
	}
	for _, combo := range snapshot.Combos {
		if combo.Enabled != nil && !*combo.Enabled {
			continue
		}
		if combo.ID != "" && !seen[combo.ID] {
			seen[combo.ID] = true
			options = append(options, combo.ID)
		}
	}
	sort.Strings(options)
	return options
}

func pickFromList(reader *bufio.Reader, prompt string, options []string) (string, error) {
	fmt.Printf("\n%s:\n", prompt)
	for index, option := range options {
		fmt.Printf("  %2d) %s\n", index+1, option)
	}
	fmt.Printf("Choice: ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	index, convErr := strconv.Atoi(strings.TrimSpace(line))
	if convErr != nil || index < 1 || index > len(options) {
		return "", fmt.Errorf("invalid selection")
	}
	return options[index-1], nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (c *setupClient) do(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if c.secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.secret)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("%s", setupErrorMessage(payload, response.StatusCode))
	}
	return payload, nil
}

func setupErrorMessage(payload []byte, statusCode int) string {
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &decoded); err == nil && decoded.Error.Message != "" {
		return decoded.Error.Message
	}
	return fmt.Sprintf("HTTP %d", statusCode)
}

func (c *setupClient) snapshot() (setupSnapshot, error) {
	payload, err := c.do(http.MethodGet, "/api/admin/snapshot", nil)
	if err != nil {
		return setupSnapshot{}, err
	}
	var snapshot setupSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return setupSnapshot{}, err
	}
	return snapshot, nil
}

func (c *setupClient) statuses() (map[string]setupToolStatus, error) {
	payload, err := c.do(http.MethodGet, "/api/admin/cli-tools/status", nil)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Statuses map[string]setupToolStatus `json:"statuses"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	return decoded.Statuses, nil
}

func (c *setupClient) status(toolID string) (setupToolStatus, error) {
	payload, err := c.do(http.MethodGet, "/api/admin/cli-tools/"+toolID, nil)
	if err != nil {
		return setupToolStatus{}, err
	}
	var status setupToolStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return setupToolStatus{}, err
	}
	return status, nil
}

func (c *setupClient) apiKeySecrets() (map[string]string, error) {
	payload, err := c.do(http.MethodGet, "/api/admin/api-key-secrets", nil)
	if err != nil {
		return map[string]string{}, nil // secret resolution is best-effort
	}
	var decoded struct {
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return map[string]string{}, nil
	}
	return decoded.Secrets, nil
}

func (c *setupClient) apply(toolID string, payload map[string]any) error {
	_, err := c.do(http.MethodPost, "/api/admin/cli-tools/"+toolID, payload)
	return err
}

func (c *setupClient) reset(toolID string) error {
	_, err := c.do(http.MethodDelete, "/api/admin/cli-tools/"+toolID, nil)
	return err
}
