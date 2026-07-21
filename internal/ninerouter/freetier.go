package ninerouter

// FreeTierEntry describes a provider's free-tier offering for the dashboard catalog.
type FreeTierEntry struct {
	ProviderID  string   `json:"provider_id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Models      []string `json:"models,omitempty"`
	DailyLimit  string   `json:"daily_limit,omitempty"`
	ResetWindow string   `json:"reset_window,omitempty"`
	AuthType    string   `json:"auth_type"`
	ApiKeyURL   string   `json:"api_key_url,omitempty"`
	HasOAuth    bool     `json:"has_oauth"`
	Notes       string   `json:"notes,omitempty"`
}

// FreeTierCatalog returns free-tier providers from the merged preset catalog.
func FreeTierCatalog() []FreeTierEntry {
	entries := make([]FreeTierEntry, 0, 32)
	for _, preset := range AllPresets() {
		if preset.Category != "freeTier" && preset.Category != "free" {
			continue
		}
		entries = append(entries, FreeTierEntry{
			ProviderID:  preset.ID,
			Name:        preset.Name,
			Category:    preset.Category,
			AuthType:    preset.AuthType,
			ApiKeyURL:   preset.ApiKeyURL,
			HasOAuth:    preset.HasOAuth,
			Models:      freeTierModels(preset.ID),
			DailyLimit:  freeTierDailyLimit(preset.ID),
			ResetWindow: freeTierReset(preset.ID),
			Notes:       freeTierNotes(preset.ID),
		})
	}
	return entries
}

func freeTierModels(providerID string) []string {
	switch providerID {
	case "gemini", "google-ai-studio":
		return []string{"gemini-2.0-flash", "gemini-2.0-flash-lite"}
	case "groq":
		return []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}
	case "openrouter":
		return []string{":free tier models"}
	case "nvidia":
		return []string{"meta/llama-3.1-8b-instruct", "mistralai/mistral-7b-instruct-v0.3"}
	case "cloudflare-ai":
		return []string{"@cf/meta/llama-3.1-8b-instruct"}
	case "ollama":
		return []string{"cloud models"}
	default:
		return nil
	}
}

func freeTierDailyLimit(providerID string) string {
	switch providerID {
	case "gemini", "google-ai-studio":
		return "1,500 RPD (Flash)"
	case "groq":
		return "14,400 RPD"
	case "nvidia":
		return "40 RPD (build.nvidia.com)"
	case "cloudflare-ai":
		return "10,000 neurons/day"
	default:
		return ""
	}
}

func freeTierReset(providerID string) string {
	switch providerID {
	case "gemini", "google-ai-studio", "groq":
		return "UTC midnight"
	case "nvidia":
		return "UTC midnight"
	default:
		return "provider-defined"
	}
}

func freeTierNotes(providerID string) string {
	switch providerID {
	case "edge-tts", "google-tts", "coqui", "tortoise":
		return "Local/no-key TTS adapter"
	case "searxng":
		return "Self-hosted search; no API key"
	case "lmstudio":
		return "Local OpenAI-compatible server"
	case "vertex":
		return "GCP free trial credits apply"
	default:
		return ""
	}
}
