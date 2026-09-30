package store

import (
	"fmt"
	"strconv"
	"time"
)

// AdmissionPolicy is account capacity, independent of OAuth token expiry.
type AdmissionPolicy struct {
	MaxConcurrent  int    `json:"max_concurrent_requests"`
	MaxQueue       int    `json:"max_queue_size"`
	QueueTimeoutMS int    `json:"queue_timeout_ms"`
	ExpiresAt      string `json:"account_expires_at,omitempty"`
}

func CredentialAdmission(metadata map[string]any) AdmissionPolicy {
	policy := AdmissionPolicy{MaxQueue: 16, QueueTimeoutMS: 5000}
	read := func(key string, fallback int) int {
		value, exists := metadata[key]
		if !exists {
			return fallback
		}
		n, err := strconv.Atoi(fmt.Sprint(value))
		if err != nil {
			return fallback
		}
		return n
	}
	policy.MaxConcurrent = read("max_concurrent_requests", read("sub2api_concurrency", 0))
	policy.MaxQueue = read("max_queue_size", 16)
	policy.QueueTimeoutMS = read("queue_timeout_ms", 5000)
	if value, ok := metadata["account_expires_at"].(string); ok {
		policy.ExpiresAt = value
	} else if value, ok := metadata["sub2api_expires_at"].(string); ok {
		policy.ExpiresAt = value
	}
	if _, explicit := metadata["account_expires_at"]; !explicit && policy.ExpiresAt == "" {
		if unix, err := strconv.ParseFloat(fmt.Sprint(metadata["sub2api_expires_at"]), 64); err == nil && unix > 0 {
			policy.ExpiresAt = time.Unix(int64(unix), 0).UTC().Format(time.RFC3339)
		}
	}
	return policy
}

func ValidateAdmissionMetadata(metadata map[string]any) error {
	for key, maximum := range map[string]int{"max_concurrent_requests": 10000, "max_queue_size": 1000, "queue_timeout_ms": 120000} {
		if value, exists := metadata[key]; exists {
			n, err := strconv.Atoi(fmt.Sprint(value))
			if err != nil || n < 0 || n > maximum {
				return fmt.Errorf("%s must be an integer between 0 and %d", key, maximum)
			}
		}
	}
	if value, exists := metadata["account_expires_at"]; exists && value != "" {
		if _, err := time.Parse(time.RFC3339, fmt.Sprint(value)); err != nil {
			return fmt.Errorf("account_expires_at must be RFC3339")
		}
	}
	return nil
}

func AccountExpired(credential Credential, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339, CredentialAdmission(credential.Metadata).ExpiresAt)
	return err == nil && !expires.After(now)
}
