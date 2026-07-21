package ninerouter

// AllPresets returns the merged preset catalog (generated + wave-2 extensions).
func AllPresets() map[string]Preset {
	merged := make(map[string]Preset, len(Presets)+len(Wave2Presets))
	for id, preset := range Presets {
		merged[id] = preset
	}
	for id, preset := range Wave2Presets {
		merged[id] = preset
	}
	return merged
}
