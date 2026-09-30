package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

const responseSessionTTL = 15 * time.Minute
const responseSessionMaxBytes = 2 << 20

type responseHistory struct {
	Payload   map[string]any
	Output    []any
	ExpiresAt time.Time
	Size      int
}
type responseSessions struct {
	mu      sync.Mutex
	entries map[string]responseHistory
	bytes   int
}

func responseSessionScope(r *http.Request, session string) string {
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	identity := []string{"local", "", session}
	if key != nil {
		identity[0] = key.ID
		identity[1] = key.Policy.Team
	}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *responseSessions) prune(now time.Time) {
	for key, item := range s.entries {
		if !item.ExpiresAt.After(now) {
			delete(s.entries, key)
			s.bytes -= item.Size
		}
	}
}

func (s *responseSessions) get(scope, id string) (responseHistory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	item, ok := s.entries[scope+":"+id]
	return item, ok
}

func (s *responseSessions) put(scope, id string, payload map[string]any, output []any) {
	data, err := json.Marshal(responseHistory{Payload: payload, Output: output})
	if err != nil || len(data) > responseSessionMaxBytes || id == "" {
		return
	}
	var item responseHistory
	_ = json.Unmarshal(data, &item) // Immutable deep copy, shared safely by lookups.
	item.Size = len(data)
	item.ExpiresAt = time.Now().Add(responseSessionTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	if s.entries == nil {
		s.entries = map[string]responseHistory{}
	}
	key := scope + ":" + id
	if old, ok := s.entries[key]; ok {
		s.bytes -= old.Size
		delete(s.entries, key)
	}
	for len(s.entries) >= 128 || s.bytes+item.Size > 32<<20 {
		oldest := ""
		for key, value := range s.entries {
			if oldest == "" || value.ExpiresAt.Before(s.entries[oldest].ExpiresAt) {
				oldest = key
			}
		}
		s.bytes -= s.entries[oldest].Size
		delete(s.entries, oldest)
	}
	s.entries[key] = item
	s.bytes += item.Size
}

func responsesInput(value any) []any {
	switch input := value.(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": input}}
	case []any:
		return input
	default:
		return nil
	}
}

func (s *responseSessions) prepare(scope, lastID string, payload map[string]any) (map[string]any, error) {
	result := cloneMap(payload)
	previous := strings.TrimSpace(stringValue(payload["previous_response_id"]))
	reset, _ := payload["reset"].(bool)
	delete(result, "reset")
	var prior responseHistory
	var ok bool
	if previous != "" && !reset {
		prior, ok = s.get(scope, previous)
		if !ok {
			return nil, fmt.Errorf("previous_response_id is unknown or expired for this client/session; resend full input without it")
		}
	} else if lastID != "" && !reset {
		prior, ok = s.get(scope, lastID)
	}
	if ok {
		for _, key := range []string{"model", "instructions", "tools", "tool_choice", "reasoning", "text", "max_output_tokens", "temperature", "parallel_tool_calls"} {
			if _, exists := result[key]; !exists {
				if value, exists := prior.Payload[key]; exists {
					result[key] = value
				}
			}
		}
	}
	input := responsesInput(payload["input"])
	if previous != "" && !reset {
		history := append(append([]any{}, responsesInput(prior.Payload["input"])...), prior.Output...)
		replace := false
		for _, value := range input {
			if item, ok := value.(map[string]any); ok && stringValue(item["type"]) == "compaction" {
				replace = true
			}
		}
		// Full-history replacement (including a compacted transcript) must not
		// prepend the old conversation a second time.
		priorInput := responsesInput(prior.Payload["input"])
		if len(priorInput) > 0 && len(input) >= len(priorInput) && reflect.DeepEqual(input[:len(priorInput)], priorInput) {
			replace = true
		}
		if !replace {
			overlap := 0
			for n := min(len(history), len(input)); n > 0; n-- {
				if reflect.DeepEqual(history[len(history)-n:], input[:n]) {
					overlap = n
					break
				}
			}
			input = append(history, input[overlap:]...)
		}
	}
	result["input"] = input
	delete(result, "previous_response_id") // Upstreams may use store=false.
	encoded, _ := json.Marshal(result)
	if len(encoded) > responseSessionMaxBytes {
		return nil, fmt.Errorf("session context exceeds 2 MiB; compact or resend a shorter transcript")
	}
	if strings.TrimSpace(stringValue(result["model"])) == "" {
		return nil, fmt.Errorf("model is required on the first turn")
	}
	calls := map[string]bool{}
	for _, value := range input {
		item, _ := value.(map[string]any)
		id := stringValue(item["call_id"])
		switch stringValue(item["type"]) {
		case "function_call":
			calls[id] = true
		case "function_call_output":
			if id == "" || !calls[id] {
				return nil, fmt.Errorf("function_call_output has no preceding function_call for %q", id)
			}
		}
	}
	var cloned map[string]any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}
