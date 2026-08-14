package cursor

import (
	"sort"
	"strings"
)

// DiscoveredModelEntry is a normalized Cursor model row for upstream discovery.
type DiscoveredModelEntry struct {
	ID                string
	Name              string
	SupportsImages    bool
	SupportsReasoning bool
}

func modelsFromParameterizedMetadata(models []parameterizedModel) []DiscoveredModelEntry {
	rows := make([]DiscoveredModelEntry, 0)
	seen := map[string]struct{}{}
	add := func(id, name string, supportsImages, supportsReasoning bool) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		if strings.EqualFold(id, "default") {
			id = "default"
			if name == "" || strings.EqualFold(name, "default") {
				name = "Auto (Server Picks)"
			}
		}
		if name == "" {
			name = id
		}
		rows = append(rows, DiscoveredModelEntry{
			ID:                id,
			Name:              name,
			SupportsImages:    supportsImages,
			SupportsReasoning: supportsReasoning,
		})
	}

	for _, model := range models {
		if len(model.Variants) == 0 {
			add(model.Name, displayName(model), model.SupportsImages, supportsReasoningModelID(model.Name))
			continue
		}

		groups := map[string]*variantGroup{}
		for _, variant := range model.Variants {
			if variant.VariantStringRepresentation != "" {
				name := firstNonEmpty(variant.DisplayNameOutsidePicker, variant.DisplayName, displayName(model))
				add(variant.VariantStringRepresentation, name, model.SupportsImages, supportsReasoningModelID(variant.VariantStringRepresentation))
				continue
			}
			if len(variant.Parameters) == 0 {
				add(model.Name, displayName(model), model.SupportsImages, supportsReasoningModelID(model.Name))
				continue
			}
			effortID := metadataEffortParameterID(variant)
			key := parameterGroupKey(variant, effortID)
			group := groups[key]
			if group == nil {
				group = &variantGroup{effortParameterID: effortID}
				groups[key] = group
			}
			group.variants = append(group.variants, variant)
		}

		for _, group := range groups {
			if len(group.variants) == 0 {
				continue
			}
			first := group.variants[0]
			for _, row := range buildParameterizedRows(model, group.variants, first.IsMaxMode, group.effortParameterID) {
				add(row.ID, row.Name, row.SupportsImages, row.SupportsReasoning)
			}
			if shouldGenerateSyntheticMaxRows(model, first) {
				for _, row := range buildParameterizedRows(model, group.variants, true, group.effortParameterID) {
					add(row.ID, row.Name, row.SupportsImages, row.SupportsReasoning)
				}
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

type variantGroup struct {
	effortParameterID string
	variants          []parameterizedVariant
}

func displayName(model parameterizedModel) string {
	return firstNonEmpty(model.ClientDisplayName, model.ServerModelName, model.Name)
}

func metadataEffortParameterID(variant parameterizedVariant) string {
	for _, parameter := range variant.Parameters {
		switch parameter.ID {
		case "reasoning":
			return "reasoning"
		case "effort":
			return "effort"
		}
	}
	return ""
}

func parameterGroupKey(variant parameterizedVariant, effortParameterID string) string {
	parts := make([]string, 0, len(variant.Parameters))
	for _, parameter := range variant.Parameters {
		if parameter.ID == effortParameterID {
			continue
		}
		parts = append(parts, parameter.ID+"="+parameter.Value)
	}
	sort.Strings(parts)
	mode := "nonmax"
	if variant.IsMaxMode {
		mode = "max"
	}
	return mode + "|" + strings.Join(parts, ";")
}

func parameterValue(parameters []modelParameter, id string) string {
	for _, parameter := range parameters {
		if parameter.ID == id {
			return parameter.Value
		}
	}
	return ""
}

func shouldGenerateSyntheticMaxRows(model parameterizedModel, variant parameterizedVariant) bool {
	return model.SupportsMaxMode && !variant.IsMaxMode
}

func buildParameterizedRows(model parameterizedModel, variants []parameterizedVariant, requestedMaxMode bool, effortParameterID string) []DiscoveredModelEntry {
	first := variants[0]
	if first.Parameters == nil {
		return nil
	}
	if requestedMaxMode && !first.IsMaxMode && !model.SupportsMaxMode {
		return nil
	}

	fast := parameterValue(first.Parameters, "fast") == "true"
	thinking := parameterValue(first.Parameters, "thinking") == "true"
	hasEffort := effortParameterID != ""
	baseID := parameterizedBaseID(model.Name, first, requestedMaxMode, hasEffort)
	baseLabel := parameterizedBaseLabel(model, first, requestedMaxMode, hasEffort)

	rows := make([]DiscoveredModelEntry, 0, len(variants))
	for _, variant := range variants {
		parameters := append([]modelParameter(nil), variant.Parameters...)
		if !hasVariantParameterSet(model, parameters) {
			continue
		}
		effort := ""
		if effortParameterID != "" {
			effort = parameterValue(parameters, effortParameterID)
			if effortParameterID == "reasoning" && (effort == "minimal" || effort == "max") {
				continue
			}
		}

		id := baseID
		if effortParameterID != "" && effort != "" {
			id = baseID + "-" + effort
		}
		if thinking {
			id += "-thinking"
		}
		if fast {
			id += "-fast"
		}

		nameParts := append([]string{}, baseLabel...)
		if effort != "" {
			nameParts = append(nameParts, cursorEffortLabel(effort))
		}
		if thinking {
			nameParts = append(nameParts, "Thinking")
		}
		if fast {
			nameParts = append(nameParts, "Fast")
		}

		rows = append(rows, DiscoveredModelEntry{
			ID:                id,
			Name:              strings.Join(compactStrings(nameParts), " "),
			SupportsImages:    model.SupportsImages,
			SupportsReasoning: effortParameterID != "" || thinking || supportsReasoningModelID(id),
		})
	}
	return rows
}

func hasVariantParameterSet(model parameterizedModel, parameters []modelParameter) bool {
	normalized := normalizeParameterValues(parameters)
	for _, variant := range model.Variants {
		if normalizeParameterValues(variant.Parameters) == normalized {
			return true
		}
	}
	return false
}

func normalizeParameterValues(parameters []modelParameter) string {
	parts := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		parts = append(parts, parameter.ID+"="+parameter.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func parameterizedBaseID(modelName string, variant parameterizedVariant, requestedMaxMode, hasEffortParameter bool) string {
	context := parameterValue(variant.Parameters, "context")
	return modelName + contextIDPart(context) + maxModeIDPart(modelName, context, requestedMaxMode, hasEffortParameter)
}

func parameterizedBaseLabel(model parameterizedModel, variant parameterizedVariant, requestedMaxMode, hasEffortParameter bool) []string {
	context := parameterValue(variant.Parameters, "context")
	parts := []string{displayName(model)}
	if label := contextLabel(context); label != "" {
		parts = append(parts, label)
	}
	if label := maxModeLabel(model.Name, context, requestedMaxMode, hasEffortParameter); label != "" {
		parts = append(parts, label)
	}
	return parts
}

func isDefaultContext(context string) bool {
	if context == "" {
		return true
	}
	switch strings.ToLower(context) {
	case "200k", "272k", "300k":
		return true
	default:
		return false
	}
}

func contextIDPart(context string) string {
	if context == "" || isDefaultContext(context) {
		return ""
	}
	return "-" + strings.ToLower(context)
}

func contextLabel(context string) string {
	if context == "" || isDefaultContext(context) {
		return ""
	}
	return strings.ToUpper(context)
}

func maxModeIDPart(modelName, context string, requestedMaxMode, hasEffortParameter bool) string {
	if !requestedMaxMode || context == "1m" {
		return ""
	}
	if !hasEffortParameter || strings.Contains(strings.ToLower(modelName), "-max") {
		return "-max-mode"
	}
	return "-max"
}

func maxModeLabel(modelName, context string, requestedMaxMode, hasEffortParameter bool) string {
	part := maxModeIDPart(modelName, context, requestedMaxMode, hasEffortParameter)
	if part == "" {
		return ""
	}
	if part == "-max-mode" {
		return "Max Mode"
	}
	return "Max"
}

func cursorEffortLabel(value string) string {
	switch value {
	case "none":
		return "None"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh", "extra-high":
		return "Extra High"
	case "max":
		return "Max"
	default:
		parts := strings.Fields(strings.ReplaceAll(value, "-", " "))
		for i, part := range parts {
			if part == "" {
				continue
			}
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
		return strings.Join(parts, " ")
	}
}

func supportsReasoningModelID(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(lower, "thinking") ||
		strings.Contains(lower, "reasoning") ||
		strings.Contains(lower, "-high") ||
		strings.Contains(lower, "-max")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
