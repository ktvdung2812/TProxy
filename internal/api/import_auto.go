package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tproxy/tproxy/internal/import9router"
	"github.com/tproxy/tproxy/internal/importcliproxy"
	"github.com/tproxy/tproxy/internal/importsub2api"
)

type autoImportResult struct {
	Source        string   `json:"source"`
	TotalAccounts int      `json:"total_accounts"`
	Result        importOK `json:"result"`
}

func (r *autoImportResult) GetOK() bool { return r.Result.GetOK() }

func (s *Server) adminImportAuto(w http.ResponseWriter, r *http.Request) {
	s.adminImportPayload(w, r, func(ctx context.Context, data []byte, dryRun bool) (importOK, error) {
		source, normalized, total, err := detectImport(data)
		if err != nil {
			return nil, err
		}
		response := &autoImportResult{Source: source, TotalAccounts: total}
		switch source {
		case "9router":
			response.Result, err = import9router.Import(ctx, s.store, normalized, import9router.Options{DryRun: dryRun})
		case "cliproxy":
			response.Result, err = importcliproxy.Import(ctx, s.store, normalized, importcliproxy.Options{DryRun: dryRun})
		case "auth-bundle":
			if dryRun {
				err = s.store.PreviewAuthBundle(ctx, normalized)
			} else {
				err = s.store.ImportAuthBundle(ctx, normalized)
			}
			response.Result = &importsub2api.Result{OK: err == nil, DryRun: dryRun, Counts: importsub2api.Counts{Credentials: total}}
		default:
			maxAccounts, _ := strconv.Atoi(r.URL.Query().Get("max_accounts"))
			skipDuplicates := !strings.EqualFold(r.URL.Query().Get("skip_duplicates"), "false") && r.URL.Query().Get("skip_duplicates") != "0"
			response.Result, err = importsub2api.Import(ctx, s.store, normalized, importsub2api.Options{DryRun: dryRun, MaxAccounts: maxAccounts, SkipDuplicates: skipDuplicates})
		}
		return response, err
	})
}

// Detection is shared by previews and imports. Never fall back to a user's
// selected format: an unrecognized file must not silently import zero records.
func detectImport(data []byte) (source string, normalized []byte, total int, err error) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return "", nil, 0, fmt.Errorf("invalid JSON import file")
	}
	record, _ := value.(map[string]any)
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			return "", nil, 0, fmt.Errorf("import file contains no accounts")
		}
		record, _ = list[0].(map[string]any)
		// Arrays may contain multiple providers, but not different export shapes.
		for _, item := range list {
			next, ok := item.(map[string]any)
			if !ok || importShape(next) != importShape(record) {
				return "", nil, 0, fmt.Errorf("mixed account formats in one array; select separate JSON files instead")
			}
		}
	}
	switch importShape(record) {
	case "9router":
		backup, parseErr := import9router.ParseBackup(data)
		if parseErr != nil {
			return "", nil, 0, parseErr
		}
		return "9router", data, len(backup.ProviderConnections), nil
	case "auth-bundle":
		credentials, _ := record["credentials"].([]any)
		if len(credentials) == 0 {
			return "", nil, 0, fmt.Errorf("auth bundle contains no credentials")
		}
		return "auth-bundle", data, len(credentials), nil
	case "cliproxy":
		files, parseErr := importcliproxy.ParseAuthFiles(data)
		if parseErr != nil {
			return "", nil, 0, parseErr
		}
		return "cliproxy", data, len(files), nil
	case "chatgpt":
		export, parseErr := importsub2api.ParseTokenExport(data)
		if parseErr != nil {
			return "", nil, 0, parseErr
		}
		normalized, err = json.Marshal(export)
		return "chatgpt", normalized, len(export.Accounts), err
	default:
		export, parseErr := importsub2api.ParseExport(data)
		if parseErr != nil {
			return "", nil, 0, fmt.Errorf("unrecognized import format: expected 9router, CLIProxyAPI, sub2api, ChatGPT OAuth tokens, or a tproxy OAuth bundle")
		}
		return "sub2api", data, len(export.Accounts), nil
	}
}

func importShape(record map[string]any) string {
	for _, key := range []string{"providerConnections", "apiKeys", "combos"} {
		if _, exists := record[key]; exists {
			return "9router"
		}
	}
	if _, ok := record["credentials"].([]any); ok {
		return "auth-bundle"
	}
	if _, exists := record["accessToken"]; exists {
		return "chatgpt"
	}
	if _, exists := record["type"]; exists {
		for _, key := range []string{"access_token", "api_key"} {
			if _, exists := record[key]; exists {
				return "cliproxy"
			}
		}
	}
	return "sub2api"
}
