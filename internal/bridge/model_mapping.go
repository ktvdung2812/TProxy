package bridge

import (
	"strings"
	"sync"
)

// AppSettingModelMappings is the app_settings key for global custom model rewrites.
const AppSettingModelMappings = "model.mapping_rules"

// maxModelMappingDepth bounds chain resolution (A→B→C…) and guards against cycles.
const maxModelMappingDepth = 8

// MaxModelMappingDepth exposes the chain depth limit to other packages.
func MaxModelMappingDepth() int { return maxModelMappingDepth }

// ModelMappings maps an exact client model ID → another model ID (virtual or
// concrete). Matching is exact on the normalized (lowercased) source name.
type ModelMappings map[string]string

// ModelMappingResolver rewrites client model IDs globally on every ingress
// before Cursor/Claude alias resolvers run. Targets may themselves be virtual
// models, tier placeholders (fable, gpt-sol, …) or provider selectors — the
// router/Provider Priority Manager decides where a target is served from.
type ModelMappingResolver struct {
	mu       sync.RWMutex
	mappings ModelMappings
}

func NewModelMappingResolver() *ModelMappingResolver {
	return &ModelMappingResolver{mappings: ModelMappings{}}
}

func (r *ModelMappingResolver) SetMappings(mappings ModelMappings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mappings = NormalizeModelMappings(mappings)
}

func (r *ModelMappingResolver) Mappings() ModelMappings {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneModelMappings(r.mappings)
}

// EffectiveMapping returns non-empty mappings keyed by normalized source name.
func (r *ModelMappingResolver) EffectiveMapping() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.mappings))
	for source, target := range r.mappings {
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
			continue
		}
		out[source] = strings.TrimSpace(target)
	}
	return out
}

// ResolveChain rewrites a client model ID through the mapping table, following
// chains up to maxModelMappingDepth hops. Unknown models pass through
// unchanged; cycles stop at the last resolvable name.
func (r *ModelMappingResolver) ResolveChain(requested string) string {
	normalized := NormalizeModel(requested)
	if normalized == "" {
		return requested
	}
	current := requested
	seen := map[string]struct{}{normalized: {}}
	for i := 0; i < maxModelMappingDepth; i++ {
		key := NormalizeModel(current)
		if key == "" {
			break
		}
		r.mu.RLock()
		target, ok := r.mappings[key]
		r.mu.RUnlock()
		if !ok || strings.TrimSpace(target) == "" {
			break
		}
		nextKey := NormalizeModel(target)
		if _, cyclic := seen[nextKey]; cyclic || nextKey == "" {
			break
		}
		seen[nextKey] = struct{}{}
		current = strings.TrimSpace(target)
	}
	return current
}

// Resolve is a single-hop rewrite kept for parity with the other resolvers.
func (r *ModelMappingResolver) Resolve(requested string) string {
	normalized := NormalizeModel(requested)
	if normalized == "" {
		return requested
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if target := strings.TrimSpace(r.mappings[normalized]); target != "" {
		return target
	}
	return requested
}

func cloneModelMappings(in ModelMappings) ModelMappings {
	if len(in) == 0 {
		return ModelMappings{}
	}
	out := make(ModelMappings, len(in))
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

// NormalizeModelMappings sanitizes a raw map from API/storage. Identity
// mappings are dropped — they are no-ops and only add confusion in the UI.
func NormalizeModelMappings(raw map[string]string) ModelMappings {
	out := make(ModelMappings, len(raw))
	for source, target := range raw {
		src := NormalizeModel(source)
		dst := strings.TrimSpace(target)
		if src == "" || dst == "" || src == NormalizeModel(dst) {
			continue
		}
		out[src] = dst
	}
	return out
}
