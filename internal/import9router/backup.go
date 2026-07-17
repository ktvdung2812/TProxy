package import9router

import "encoding/json"

// Backup mirrors the JSON payload exported by 9router /api/settings/database.
type Backup struct {
	Settings            map[string]any       `json:"settings"`
	ProviderConnections []ProviderConnection `json:"providerConnections"`
	ProviderNodes       []json.RawMessage    `json:"providerNodes"`
	ProxyPools          []ProxyPool          `json:"proxyPools"`
	APIKeys             []APIKey             `json:"apiKeys"`
	Combos              []Combo              `json:"combos"`
	ModelAliases        map[string]string    `json:"modelAliases"`
	CustomModels        []CustomModel        `json:"customModels"`
	MitmAlias           map[string]any       `json:"mitmAlias"`
	Pricing             map[string]any       `json:"pricing"`
}

type ProviderConnection struct {
	ID                   string         `json:"id"`
	Provider             string         `json:"provider"`
	AuthType             string         `json:"authType"`
	Name                 string         `json:"name"`
	Email                string         `json:"email"`
	Priority             int            `json:"priority"`
	IsActive             bool           `json:"isActive"`
	AccessToken          string         `json:"accessToken"`
	RefreshToken         string         `json:"refreshToken"`
	ExpiresAt            string         `json:"expiresAt"`
	Scope                string         `json:"scope"`
	APIKey               string         `json:"apiKey"`
	ProviderSpecificData map[string]any `json:"providerSpecificData"`
}

type ProxyPool struct {
	ID       string `json:"id"`
	IsActive bool   `json:"isActive"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

type APIKey struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	MachineID string `json:"machineId"`
	IsActive  bool   `json:"isActive"`
}

type Combo struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Models []string `json:"models"`
}

type CustomModel struct {
	ProviderAlias string `json:"providerAlias"`
	ID            string `json:"id"`
	Type          string `json:"type"`
	Name          string `json:"name"`
}
