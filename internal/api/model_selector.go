package api

import "strings"

type modelSelector struct {
	Model    string
	Provider string
}

func parseModelSelector(value string) modelSelector {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return modelSelector{}
	}
	if provider, model, ok := strings.Cut(trimmed, "::"); ok && provider != "" && model != "" {
		return modelSelector{Model: strings.TrimSpace(model), Provider: strings.TrimSpace(provider)}
	}
	if provider, model, ok := strings.Cut(trimmed, ":"); ok && provider != "" && model != "" {
		return modelSelector{Model: strings.TrimSpace(model), Provider: strings.TrimSpace(provider)}
	}
	return modelSelector{Model: trimmed}
}

func splitModelSelector(requested string) (provider, model string, ok bool) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", "", false
	}
	if provider, model, ok := strings.Cut(trimmed, "::"); ok && provider != "" && model != "" {
		return strings.TrimSpace(provider), strings.TrimSpace(model), true
	}
	if provider, model, ok := strings.Cut(trimmed, ":"); ok && provider != "" && model != "" {
		return strings.TrimSpace(provider), strings.TrimSpace(model), true
	}
	return "", "", false
}

func formatProviderModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return model
	}
	return provider + ":" + model
}
