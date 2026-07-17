package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tproxy/tproxy/internal/security"
)

type credentialGateError struct {
	Code    string
	Message string
}

func (e *credentialGateError) Error() string { return e.Message }

func rejectQueryCredentials(r *http.Request) error {
	for _, parameter := range []string{"token", "api_key", "admin_key", "access_token"} {
		if r.URL.Query().Has(parameter) {
			return &credentialGateError{
				Code:    "query_credential_rejected",
				Message: "credentials in URL query parameters are not accepted",
			}
		}
	}
	return nil
}

func extractClientAPIKey(r *http.Request) (string, error) {
	if err := rejectQueryCredentials(r); err != nil {
		return "", err
	}
	if strings.TrimSpace(r.Header.Get("X-Admin-Key")) != "" {
		return "", &credentialGateError{
			Code:    "admin_key_rejected",
			Message: "legacy admin-key authentication is not accepted",
		}
	}
	apiKey := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	bearer := security.BearerToken(r)
	if apiKey != "" && bearer != "" && !security.ConstantTimeEqual(apiKey, bearer) {
		return "", &credentialGateError{
			Code:    "ambiguous_credentials",
			Message: "conflicting API credentials were supplied",
		}
	}
	if apiKey != "" {
		return apiKey, nil
	}
	return bearer, nil
}

func credentialGateStatus(err error) (int, string, string) {
	var gateErr *credentialGateError
	if errors.As(err, &gateErr) {
		switch gateErr.Code {
		case "query_credential_rejected", "ambiguous_credentials", "admin_key_rejected":
			return http.StatusBadRequest, gateErr.Code, gateErr.Message
		default:
			return http.StatusUnauthorized, gateErr.Code, gateErr.Message
		}
	}
	return http.StatusUnauthorized, "invalid_api_key", err.Error()
}
