package models

import "time"

// ModelProviders lists every provider a model can be routed through.
var ModelProviders = []ModelProvider{
	ModelProviderOpenAI,
	ModelProviderAnthropic,
	ModelProviderZAI,
	ModelProviderGoogle,
	ModelProviderMistral,
	ModelProviderDeepSeek,
	ModelProviderQwen,
	ModelProviderXiaomi,
}

// Known reports whether p is one of ModelProviders.
func (p ModelProvider) Known() bool {
	for _, known := range ModelProviders {
		if p == known {
			return true
		}
	}
	return false
}

// ProviderKeySummary describes a stored provider key without revealing it.
type ProviderKeySummary struct {
	Provider ModelProvider `json:"provider"`
	// Hint is the key's last four characters.
	Hint      string    `json:"hint"`
	UpdatedAt time.Time `json:"updated_at"`
}
