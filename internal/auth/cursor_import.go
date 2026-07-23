package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tproxy/tproxy/internal/store"
	_ "modernc.org/sqlite"
)

const (
	cursorAccessTokenKey = "cursorAuth/accessToken"
	cursorTokenKey       = "cursorAuth/token"
	cursorMachineIDKey   = "storage.serviceMachineId"
)

type CursorImportTokens struct {
	AccessToken string
	MachineID   string
	DBPath      string
}

type CursorAutoImportResult struct {
	Tokens        CursorImportTokens
	Found         bool
	WindowsManual bool
	Err           error
}

var cursorMachineIDPattern = regexp.MustCompile(`^[a-f0-9-]{32,}$`)

func CursorDBCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(home, "Library/Application Support/Cursor - Insiders/User/globalStorage/state.vscdb"),
		}
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return []string{
			filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb"),
			filepath.Join(appData, "Cursor - Insiders", "User", "globalStorage", "state.vscdb"),
			filepath.Join(localAppData, "Cursor", "User", "globalStorage", "state.vscdb"),
			filepath.Join(localAppData, "Programs", "Cursor", "User", "globalStorage", "state.vscdb"),
		}
	default:
		return []string{
			filepath.Join(home, ".config/Cursor/User/globalStorage/state.vscdb"),
			filepath.Join(home, ".config/cursor/User/globalStorage/state.vscdb"),
		}
	}
}

func AutoImportCursorTokens() (CursorImportTokens, error) {
	result := AutoImportCursor()
	if result.Err != nil {
		return result.Tokens, result.Err
	}
	return result.Tokens, nil
}

func AutoImportCursor() CursorAutoImportResult {
	var dbPath string
	for _, candidate := range CursorDBCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			dbPath = candidate
			break
		}
	}
	if dbPath == "" {
		return CursorAutoImportResult{
			Err: fmt.Errorf("cursor database not found; open Cursor IDE at least once"),
		}
	}

	if runtime.GOOS == "linux" && !linuxCursorInstalled() {
		return CursorAutoImportResult{
			Tokens: CursorImportTokens{DBPath: dbPath},
			Err:    fmt.Errorf("cursor database found but Cursor IDE does not appear to be installed"),
		}
	}

	tokens, err := readCursorTokensFromDB(dbPath)
	tokens.DBPath = dbPath
	if err != nil {
		return CursorAutoImportResult{Tokens: tokens, WindowsManual: true, Err: err}
	}
	if tokens.AccessToken == "" || tokens.MachineID == "" {
		return CursorAutoImportResult{
			Tokens:        tokens,
			WindowsManual: true,
			Err:           fmt.Errorf("cursor tokens missing; sign in to Cursor IDE first"),
		}
	}
	if err := ValidateCursorImportToken(tokens.AccessToken, tokens.MachineID); err != nil {
		return CursorAutoImportResult{Tokens: tokens, Err: err}
	}
	return CursorAutoImportResult{Tokens: tokens, Found: true}
}

func ValidateCursorImportToken(accessToken, machineID string) error {
	accessToken = strings.TrimSpace(accessToken)
	machineID = strings.TrimSpace(machineID)
	if accessToken == "" {
		return fmt.Errorf("access token is required")
	}
	if machineID == "" {
		return fmt.Errorf("machine ID is required")
	}
	if len(accessToken) < 50 {
		return fmt.Errorf("invalid token format: token appears too short")
	}
	normalizedMachineID := strings.ReplaceAll(machineID, "-", "")
	if !cursorMachineIDPattern.MatchString(normalizedMachineID) {
		return fmt.Errorf("invalid machine ID format: expected UUID format")
	}
	return nil
}

func linuxCursorInstalled() bool {
	if _, err := exec.LookPath("cursor"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	desktopFile := filepath.Join(home, ".local/share/applications/cursor.desktop")
	_, err = os.Stat(desktopFile)
	return err == nil
}

func readCursorTokensFromDB(dbPath string) (CursorImportTokens, error) {
	tokens, err := readCursorTokensViaSQL(dbPath)
	if err == nil && tokens.AccessToken != "" && tokens.MachineID != "" {
		return tokens, nil
	}
	return readCursorTokensViaCLI(dbPath)
}

func readCursorTokensViaSQL(dbPath string) (CursorImportTokens, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return CursorImportTokens{}, err
	}
	defer db.Close()

	accessToken, err := queryCursorItem(db, cursorAccessTokenKey, cursorTokenKey)
	if err != nil {
		return CursorImportTokens{}, err
	}
	machineID, err := queryCursorItem(db, cursorMachineIDKey, "storage.machineId", "telemetry.machineId")
	if err != nil {
		return CursorImportTokens{}, err
	}
	return CursorImportTokens{AccessToken: accessToken, MachineID: machineID}, nil
}

func queryCursorItem(db *sql.DB, keys ...string) (string, error) {
	for _, key := range keys {
		var raw string
		err := db.QueryRow(`SELECT value FROM itemTable WHERE key = ? LIMIT 1`, key).Scan(&raw)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", err
		}
		if value := normalizeCursorDBValue(raw); value != "" {
			return value, nil
		}
	}
	return "", nil
}

func readCursorTokensViaCLI(dbPath string) (CursorImportTokens, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return CursorImportTokens{}, fmt.Errorf("sqlite3 CLI not available")
	}
	accessToken := ""
	for _, key := range []string{cursorAccessTokenKey, cursorTokenKey} {
		value, err := queryCursorItemCLI(dbPath, key)
		if err != nil {
			return CursorImportTokens{}, err
		}
		if value != "" {
			accessToken = value
			break
		}
	}
	machineID := ""
	for _, key := range []string{cursorMachineIDKey, "storage.machineId", "telemetry.machineId"} {
		value, err := queryCursorItemCLI(dbPath, key)
		if err != nil {
			return CursorImportTokens{}, err
		}
		if value != "" {
			machineID = value
			break
		}
	}
	return CursorImportTokens{AccessToken: accessToken, MachineID: machineID}, nil
}

func queryCursorItemCLI(dbPath, key string) (string, error) {
	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf(`SELECT value FROM itemTable WHERE key='%s' LIMIT 1`, strings.ReplaceAll(key, "'", "''")))
	output, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return normalizeCursorDBValue(strings.TrimSpace(string(output))), nil
}

func normalizeCursorDBValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var parsed string
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return strings.TrimSpace(parsed)
	}
	return raw
}

func BuildCursorOAuthToken(accessToken, machineID string) store.OAuthToken {
	accessToken = strings.TrimSpace(accessToken)
	machineID = strings.TrimSpace(machineID)
	return store.OAuthToken{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		Extra: map[string]any{
			"machine_id":   machineID,
			"machineId":    machineID,
			"auth_method":  "imported",
			"client_type":  "ide",
			"client_version": "3.1.0",
		},
	}
}
