package store

import "time"

const (
	quotaAutoDisabledKey   = "quota_auto_disabled"
	quotaAutoDisabledAtKey = "quota_auto_disabled_at"
)

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return value == "true" || value == "1"
	default:
		return false
	}
}

// QuotaAutoDisabled reports whether the credential was disabled automatically due to quota.
func QuotaAutoDisabled(metadata map[string]any) bool {
	return metadataBool(metadata, quotaAutoDisabledKey)
}

func cloneMetadata(metadata map[string]any) map[string]any {
	cloned := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func quotaAutoDisabledMetadata(metadata map[string]any, disabled bool) map[string]any {
	cloned := cloneMetadata(metadata)
	if disabled {
		cloned[quotaAutoDisabledKey] = true
		cloned[quotaAutoDisabledAtKey] = time.Now().UTC().Format(time.RFC3339Nano)
		return cloned
	}
	delete(cloned, quotaAutoDisabledKey)
	delete(cloned, quotaAutoDisabledAtKey)
	return cloned
}
