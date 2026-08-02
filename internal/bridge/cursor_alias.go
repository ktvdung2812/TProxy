package bridge

import (
	"strings"
	"sync"
)

// AppSettingCursorAliases is the app_settings key for Cursor client model rewrites.
const AppSettingCursorAliases = "cursor.alias_models"

// CursorModel describes a known Cursor client model ID (source side of a mapping).
type CursorModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CursorAliases maps Cursor model ID → tproxy virtual model or provider:upstream selector.
type CursorAliases map[string]string

// CursorResolver rewrites Cursor model names on OpenAI-compatible ingress.
type CursorResolver struct {
	mu      sync.RWMutex
	aliases CursorAliases
}

func NewCursorResolver() *CursorResolver {
	return &CursorResolver{aliases: CursorAliases{}}
}

func (r *CursorResolver) SetAliases(aliases CursorAliases) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases = cloneCursorAliases(aliases)
}

func (r *CursorResolver) Aliases() CursorAliases {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneCursorAliases(r.aliases)
}

// Resolve rewrites a client model ID when a Cursor alias is configured.
// Unknown models pass through unchanged. Matching is case-insensitive on the client name.
func (r *CursorResolver) Resolve(requested string) string {
	normalized := NormalizeModel(requested)
	if normalized == "" {
		return requested
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if target := strings.TrimSpace(r.aliases[normalized]); target != "" {
		return target
	}
	return requested
}

// EffectiveMapping returns non-empty aliases with resolved client keys.
func (r *CursorResolver) EffectiveMapping() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.aliases))
	for source, target := range r.aliases {
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
			continue
		}
		out[source] = strings.TrimSpace(target)
	}
	return out
}

// PlaceholderRows returns only configured mappings (source → target) for the dashboard.
func (r *CursorResolver) PlaceholderRows(catalog []CursorModel) []map[string]string {
	effective := r.EffectiveMapping()
	labels := map[string]string{}
	for _, model := range catalog {
		id := NormalizeModel(model.ID)
		if id == "" {
			continue
		}
		if name := strings.TrimSpace(model.Name); name != "" {
			labels[id] = name
		} else {
			labels[id] = id
		}
	}
	keys := make([]string, 0, len(effective))
	for source := range effective {
		keys = append(keys, source)
	}
	sortCursorKeys(keys)
	rows := make([]map[string]string, 0, len(keys))
	for _, source := range keys {
		label := labels[source]
		if label == "" {
			label = source
		}
		rows = append(rows, map[string]string{
			"name":     source,
			"role":     "cursor",
			"label":    label,
			"resolves": effective[source],
		})
	}
	return rows
}

func cloneCursorAliases(in CursorAliases) CursorAliases {
	if len(in) == 0 {
		return CursorAliases{}
	}
	out := make(CursorAliases, len(in))
	for source, target := range in {
		src := NormalizeModel(source)
		dst := strings.TrimSpace(target)
		if src == "" || dst == "" {
			continue
		}
		out[src] = dst
	}
	return out
}

func sortCursorKeys(keys []string) {
	// Simple insertion sort — alias maps stay small.
	for i := 1; i < len(keys); i++ {
		j := i
		for j > 0 && keys[j-1] > keys[j] {
			keys[j-1], keys[j] = keys[j], keys[j-1]
			j--
		}
	}
}

// NormalizeCursorAliases sanitizes a raw map from API/storage.
// Identity mappings (same source/target id) are kept — operators often create a
// public model with the same id as the Cursor model name for transparent routing.
func NormalizeCursorAliases(raw map[string]string) CursorAliases {
	out := make(CursorAliases, len(raw))
	for source, target := range raw {
		src := NormalizeModel(source)
		dst := strings.TrimSpace(target)
		if src == "" || dst == "" {
			continue
		}
		out[src] = dst
	}
	return out
}
