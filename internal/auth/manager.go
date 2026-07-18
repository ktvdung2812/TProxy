package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

const (
	defaultSessionTTL    = 10 * time.Minute
	defaultRefreshWindow = 2 * time.Minute
)

const oauthCallbackSuccessHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>tproxy OAuth</title>
</head>
<body>
<h1>Authorization complete</h1>
<p>The credential is ready. This window will close in a few seconds.</p>
<script>window.setTimeout(function(){window.close()},3000)</script>
</body>
</html>`

type StartRequest struct {
	ProviderID   string `json:"provider_id"`
	CredentialID string `json:"credential_id"`
	Label        string `json:"label"`
	Email        string `json:"email"`
	Mode         string `json:"mode"`
	RedirectURL  string `json:"redirect_url"`
}

type StartResponse struct {
	SessionID        string    `json:"session_id"`
	ProviderID       string    `json:"provider_id"`
	CredentialID     string    `json:"credential_id"`
	Mode             string    `json:"mode"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
	UserCode         string    `json:"user_code,omitempty"`
	VerificationURI  string    `json:"verification_uri,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	IntervalSeconds  int       `json:"interval_seconds,omitempty"`
}

type SessionStatus struct {
	SessionID    string    `json:"session_id"`
	ProviderID   string    `json:"provider_id"`
	CredentialID string    `json:"credential_id"`
	Mode         string    `json:"mode"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expires_at"`
	ConsumedAt   time.Time `json:"consumed_at,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
}

type CredentialStatus struct {
	CredentialID string     `json:"credential_id"`
	ProviderID   string     `json:"provider_id"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	TokenType    string     `json:"token_type,omitempty"`
}

type Error struct {
	code      string
	permanent bool
	err       error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch e.code {
	case "invalid_state":
		return "OAuth state is invalid, expired or already consumed"
	case "authorization_required":
		return "OAuth authorization is required"
	case "oauth_refresh_failed":
		return "OAuth token refresh failed"
	case "oauth_provider_unavailable":
		return "OAuth provider is unavailable"
	case "oauth_configuration_invalid":
		return "OAuth provider configuration is invalid"
	case "authorization_pending":
		return "OAuth device authorization is pending"
	case "oauth_authorization_rejected":
		return "OAuth authorization was rejected"
	default:
		if e.err != nil {
			return e.err.Error()
		}
		return "OAuth operation failed"
	}
}

func (e *Error) Unwrap() error { return e.err }

func (e *Error) Code() string {
	if e == nil {
		return "oauth_error"
	}
	return e.code
}

func Code(err error) string {
	var target *Error
	if errors.As(err, &target) && target.code != "" {
		return target.code
	}
	return "oauth_error"
}

func IsPermanent(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.permanent
}

type session struct {
	mu             sync.Mutex
	id             string
	providerID     string
	credentialID   string
	label          string
	email          string
	mode           string
	state          string
	verifier       string
	redirectURL    string
	deviceCode     string
	deviceUserCode string
	interval       time.Duration
	expiresAt      time.Time
	status         string
	consumedAt     time.Time
	errorCode      string
	cancel         chan struct{}
	cancelClosed   bool
	callbackServer *http.Server
}

type refreshCall struct {
	done       chan struct{}
	credential store.Credential
	err        error
}

type discoveryEntry struct {
	config    config.OAuthConfig
	expiresAt time.Time
}

type Manager struct {
	store   *store.Store
	client  *http.Client
	rootCtx context.Context
	cancel  context.CancelFunc

	mu        sync.Mutex
	sessions  map[string]*session
	refresh   map[string]*refreshCall
	discovery map[string]discoveryEntry
	now       func() time.Time

	backgroundOnce sync.Once
	backgroundWG   sync.WaitGroup
	prewarm        *PrewarmManager
}

func NewManager(dataStore *store.Store, client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: dataStore, client: client, rootCtx: ctx, cancel: cancel, sessions: make(map[string]*session), refresh: make(map[string]*refreshCall), discovery: make(map[string]discoveryEntry), now: time.Now, prewarm: NewPrewarmManager()}
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.backgroundOnce.Do(func() {
		m.backgroundWG.Add(1)
		go func() {
			defer m.backgroundWG.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					m.refreshExpiring(m.rootCtx)
				case <-ctx.Done():
					m.cancel()
					return
				case <-m.rootCtx.Done():
					return
				}
			}
		}()
		if m.prewarm != nil {
			m.prewarm.Start(m.rootCtx, m.prewarmRefresh)
		}
	})
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	// Cancel request contexts before waiting on prewarm workers so an in-flight
	// token exchange can terminate promptly during gateway shutdown.
	m.cancel()
	if m.prewarm != nil {
		m.prewarm.Stop()
	}
	m.mu.Lock()
	for _, item := range m.sessions {
		m.stopSession(item)
	}
	m.mu.Unlock()
	m.backgroundWG.Wait()
}

// PurgeExpiredSessions expires abandoned OAuth flows and bounds the in-memory
// session map after terminal flows have exceeded the requested retention.
func (m *Manager) PurgeExpiredSessions(retentions ...time.Duration) {
	if m == nil {
		return
	}
	now := m.now()
	retention := 24 * time.Hour
	if len(retentions) > 0 && retentions[0] > 0 {
		retention = retentions[0]
	}
	var expired []*session
	m.mu.Lock()
	for id, item := range m.sessions {
		item.mu.Lock()
		status := item.status
		expiresAt := item.expiresAt
		if now.After(expiresAt) && sessionCanExpire(status) {
			item.status = "expired"
			item.errorCode = "invalid_state"
			clearSessionSecretsLocked(item)
			status = item.status
			expired = append(expired, item)
		}
		if sessionIsTerminal(status) && now.After(expiresAt.Add(retention)) {
			delete(m.sessions, id)
		}
		item.mu.Unlock()
	}
	m.mu.Unlock()
	// Callback shutdown can wait on the HTTP server, so do it without holding
	// the manager lock used by status and callback lookups.
	for _, item := range expired {
		m.stopSession(item)
	}
}

func (m *Manager) resolveOAuthConfig(ctx context.Context, provider store.Provider) (*config.OAuthConfig, error) {
	if provider.OAuth == nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	resolved := *provider.OAuth
	if strings.TrimSpace(resolved.DiscoveryURL) == "" {
		return &resolved, nil
	}
	m.mu.Lock()
	if entry, ok := m.discovery[provider.ID]; ok && entry.expiresAt.After(m.now()) {
		m.mu.Unlock()
		copy := entry.config
		return &copy, nil
	}
	m.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved.DiscoveryURL, nil)
	if err != nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return nil, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &Error{code: "oauth_provider_unavailable"}
	}
	var discovery struct {
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
		AuthorizationEndpoint       string `json:"authorization_endpoint"`
	}
	if err = json.Unmarshal(data, &discovery); err != nil {
		return nil, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid OAuth discovery response")}
	}
	if discovery.TokenEndpoint == "" {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true, err: errors.New("OAuth discovery response is missing the token endpoint")}
	}
	if err = validateDiscoveredEndpoint(resolved.DiscoveryURL, discovery.TokenEndpoint, provider.Type); err != nil {
		return nil, &Error{code: "oauth_configuration_invalid", permanent: true, err: err}
	}
	if discovery.DeviceAuthorizationEndpoint != "" {
		if err = validateDiscoveredEndpoint(resolved.DiscoveryURL, discovery.DeviceAuthorizationEndpoint, provider.Type); err != nil {
			return nil, &Error{code: "oauth_configuration_invalid", permanent: true, err: err}
		}
		resolved.DeviceCodeURL = discovery.DeviceAuthorizationEndpoint
	}
	if discovery.AuthorizationEndpoint != "" {
		if err = validateDiscoveredEndpoint(resolved.DiscoveryURL, discovery.AuthorizationEndpoint, provider.Type); err != nil {
			return nil, &Error{code: "oauth_configuration_invalid", permanent: true, err: err}
		}
		if resolved.AuthorizationURL == "" {
			resolved.AuthorizationURL = discovery.AuthorizationEndpoint
		}
	}
	resolved.TokenURL = discovery.TokenEndpoint
	m.mu.Lock()
	m.discovery[provider.ID] = discoveryEntry{config: resolved, expiresAt: m.now().Add(time.Hour)}
	m.mu.Unlock()
	return &resolved, nil
}

func validateDiscoveredEndpoint(discoveryURL, endpoint, providerType string) error {
	discovery, err := url.Parse(discoveryURL)
	if err != nil {
		return errors.New("invalid OAuth discovery URL")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
		return errors.New("OAuth discovery returned an unsafe endpoint")
	}
	if parsed.Scheme != discovery.Scheme && !(isLoopbackHost(parsed.Hostname()) && isLoopbackHost(discovery.Hostname())) {
		return errors.New("OAuth discovery endpoint scheme does not match discovery transport")
	}
	if providerType == "xai" {
		host := strings.ToLower(parsed.Hostname())
		if !isLoopbackHost(host) && host != "x.ai" && !strings.HasSuffix(host, ".x.ai") {
			return errors.New("xAI discovery endpoint is outside the x.ai domain")
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (m *Manager) StartAuthorization(ctx context.Context, request StartRequest) (StartResponse, error) {
	if m == nil || m.store == nil {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid"}
	}
	provider, err := m.store.Provider(ctx, request.ProviderID)
	if err != nil {
		return StartResponse{}, err
	}
	if !provider.Enabled || provider.OAuth == nil {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid"}
	}
	oauthConfig, err := m.resolveOAuthConfig(ctx, *provider)
	if err != nil {
		return StartResponse{}, err
	}
	if m.clientID(*oauthConfig) == "" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth client ID is unavailable")}
	}
	if oauthConfig.RequireClientSecret && m.clientSecret(*oauthConfig) == "" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth client secret is unavailable")}
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		if provider.Type == "xai" || provider.Type == "kimi" {
			mode = "device"
		} else if oauthConfig.AuthorizationURL != "" {
			mode = "browser"
		} else {
			mode = "device"
		}
	}
	if mode != "browser" && mode != "device" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth mode must be browser or device")}
	}
	credentialID := strings.TrimSpace(request.CredentialID)
	if credentialID == "" {
		credentialID = security.NewID("oauth_cred_")
	}
	sessionID := security.NewID("oauth_session_")
	expiresAt := m.now().Add(defaultSessionTTL)
	item := &session{id: sessionID, providerID: provider.ID, credentialID: credentialID, label: request.Label, email: request.Email, mode: mode, expiresAt: expiresAt, status: "pending", interval: 5 * time.Second, cancel: make(chan struct{})}
	response := StartResponse{SessionID: sessionID, ProviderID: provider.ID, CredentialID: credentialID, Mode: mode, ExpiresAt: expiresAt}
	if mode == "browser" {
		if oauthConfig.AuthorizationURL == "" {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("browser OAuth requires authorization-url")}
		}
		redirectURL := strings.TrimSpace(oauthConfig.RedirectURL)
		if redirectURL == "" {
			redirectURL = strings.TrimSpace(request.RedirectURL)
		}
		if redirectURL == "" {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth redirect URL is required")}
		}
		if err := validateRedirectURL(redirectURL); err != nil {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: err}
		}
		verifier, err := pkceVerifier()
		if err != nil {
			return StartResponse{}, err
		}
		item.verifier = verifier
		item.state = security.NewID("oauth_state_")
		item.redirectURL = redirectURL
		item.status = "pending"
		response.AuthorizationURL = authorizationURL(*oauthConfig, item.state, verifier, redirectURL, m.clientID(*oauthConfig))
	} else {
		if oauthConfig.DeviceCodeURL == "" {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("device OAuth requires device-code-url")}
		}
		device, err := m.requestDeviceCode(ctx, *oauthConfig)
		if err != nil {
			return StartResponse{}, err
		}
		item.deviceCode = device.Code
		item.deviceUserCode = device.UserCode
		item.interval = device.Interval
		if device.ExpiresIn > 0 {
			item.expiresAt = m.now().Add(device.ExpiresIn)
			response.ExpiresAt = item.expiresAt
		}
		item.status = "polling"
		response.UserCode = device.UserCode
		response.VerificationURI = device.VerificationURI
		response.IntervalSeconds = int(device.Interval / time.Second)
		if response.IntervalSeconds <= 0 {
			response.IntervalSeconds = 5
		}
	}
	m.mu.Lock()
	m.sessions[item.id] = item
	m.mu.Unlock()
	if mode == "browser" && oauthConfig.ListenForCallback {
		if err := m.startLocalCallback(item); err != nil {
			m.mu.Lock()
			delete(m.sessions, item.id)
			m.mu.Unlock()
			return StartResponse{}, err
		}
	}
	if mode == "device" {
		m.backgroundWG.Add(1)
		go func() {
			defer m.backgroundWG.Done()
			m.pollDevice(item)
		}()
	}
	return response, nil
}

func (m *Manager) startLocalCallback(item *session) error {
	parsed, err := url.Parse(item.redirectURL)
	if err != nil || parsed.Port() == "" || parsed.Path == "" {
		return &Error{code: "oauth_configuration_invalid", permanent: true, err: errors.New("local OAuth callback URL requires a port and path")}
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1" {
		return &Error{code: "oauth_configuration_invalid", permanent: true, err: errors.New("local OAuth callback listener must use a loopback host")}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(parsed.Hostname(), parsed.Port()))
	if err != nil {
		return &Error{code: "oauth_callback_unavailable", err: errors.New("OAuth callback port is unavailable")}
	}
	mux := http.NewServeMux()
	mux.HandleFunc(parsed.Path, func(w http.ResponseWriter, r *http.Request) {
		state, code, providerError, parseErr := localCallbackValues(r)
		if parseErr != nil {
			http.Error(w, "Invalid OAuth callback", http.StatusBadRequest)
			return
		}
		if providerError != "" {
			_, rejectErr := m.rejectSessionCallback(item, state, providerError)
			if Code(rejectErr) == "invalid_state" {
				http.Error(w, rejectErr.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, rejectErr.Error(), http.StatusBadRequest)
			return
		}
		_, completeErr := m.CompleteCallback(r.Context(), state, code)
		if completeErr != nil {
			http.Error(w, completeErr.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(oauthCallbackSuccessHTML))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	item.mu.Lock()
	item.callbackServer = server
	item.mu.Unlock()
	m.backgroundWG.Add(1)
	go func() {
		defer m.backgroundWG.Done()
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			m.failSession(item, "oauth_callback_unavailable")
		}
	}()
	return nil
}

func localCallbackValues(r *http.Request) (state, code, providerError string, err error) {
	if r == nil {
		return "", "", "", errors.New("OAuth callback request is missing")
	}
	switch r.Method {
	case http.MethodGet:
		values := r.URL.Query()
		return values.Get("state"), values.Get("code"), values.Get("error"), nil
	case http.MethodPost:
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			var payload struct {
				State string `json:"state"`
				Code  string `json:"code"`
				Error string `json:"error"`
			}
			if decodeErr := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); decodeErr != nil {
				return "", "", "", decodeErr
			}
			return strings.TrimSpace(payload.State), strings.TrimSpace(payload.Code), strings.TrimSpace(payload.Error), nil
		}
		if parseErr := r.ParseForm(); parseErr != nil {
			return "", "", "", parseErr
		}
		return r.PostForm.Get("state"), r.PostForm.Get("code"), r.PostForm.Get("error"), nil
	default:
		return "", "", "", errors.New("OAuth callback method is unsupported")
	}
}

func (m *Manager) CompleteCallback(ctx context.Context, state, code string) (SessionStatus, error) {
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	if state == "" || code == "" {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	item := m.sessionForState(state)
	if item == nil {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	item.mu.Lock()
	if item.status != "pending" {
		item.mu.Unlock()
		return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
	}
	if m.now().After(item.expiresAt) {
		item.status = "expired"
		item.errorCode = "invalid_state"
		clearSessionSecretsLocked(item)
		item.mu.Unlock()
		m.stopSession(item)
		return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
	}
	item.status = "consumed"
	item.consumedAt = m.now()
	providerID, credentialID, verifier, redirectURL, expectedState := item.providerID, item.credentialID, item.verifier, item.redirectURL, item.state
	label, email := item.label, item.email
	item.mu.Unlock()

	provider, err := m.store.Provider(ctx, providerID)
	if err != nil || provider.OAuth == nil {
		m.failSession(item, "oauth_provider_unavailable")
		if err == nil {
			err = &Error{code: "oauth_configuration_invalid", permanent: true}
		}
		return m.statusFor(item), err
	}
	oauthConfig, err := m.resolveOAuthConfig(ctx, *provider)
	if err != nil {
		m.failSession(item, Code(err))
		return m.statusFor(item), err
	}
	if parts := strings.SplitN(code, "#", 2); len(parts) == 2 {
		if parts[1] != "" && parts[1] != expectedState {
			m.failSession(item, "invalid_state")
			return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
		}
		code = parts[0]
	}
	token, err := m.exchangeCode(ctx, *oauthConfig, code, expectedState, verifier, redirectURL)
	if err != nil {
		m.failSession(item, Code(err))
		return m.statusFor(item), err
	}
	token, email, err = m.prepareProviderToken(ctx, *provider, token, email)
	if err != nil {
		m.failSession(item, Code(err))
		return m.statusFor(item), err
	}
	if !m.beginSessionCommit(item, "consumed") {
		return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
	}
	if err = m.store.SaveOAuthCredential(ctx, providerID, credentialID, label, email, token); err != nil {
		m.failSession(item, "oauth_credential_save_failed")
		return m.statusFor(item), err
	}
	_ = m.store.SyncProviderHealth(ctx, providerID)
	m.completeSession(item)
	return m.statusFor(item), nil
}

// RejectCallback validates an OAuth denial against the pending session state,
// records a terminal rejection, and consumes all authorization material.
func (m *Manager) RejectCallback(state, providerError string) (SessionStatus, error) {
	state = strings.TrimSpace(state)
	providerError = strings.TrimSpace(providerError)
	if state == "" || providerError == "" {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	item := m.sessionForState(state)
	if item == nil {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	return m.rejectSessionCallback(item, state, providerError)
}

func (m *Manager) sessionForState(state string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, candidate := range m.sessions {
		candidate.mu.Lock()
		candidateState := candidate.state
		candidate.mu.Unlock()
		if candidateState != "" && security.ConstantTimeEqual(candidateState, state) {
			return candidate
		}
	}
	return nil
}

func (m *Manager) rejectSessionCallback(item *session, state, providerError string) (SessionStatus, error) {
	state = strings.TrimSpace(state)
	providerError = strings.TrimSpace(providerError)
	if item == nil || state == "" || providerError == "" {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	now := m.now()
	item.mu.Lock()
	if item.status != "pending" || item.state == "" || !security.ConstantTimeEqual(item.state, state) {
		item.mu.Unlock()
		return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
	}
	if now.After(item.expiresAt) {
		item.status = "expired"
		item.errorCode = "invalid_state"
		clearSessionSecretsLocked(item)
		item.mu.Unlock()
		m.stopSession(item)
		return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
	}
	item.status = "failed"
	item.errorCode = "oauth_authorization_rejected"
	item.consumedAt = now
	clearSessionSecretsLocked(item)
	item.mu.Unlock()
	m.stopSession(item)
	return m.statusFor(item), &Error{code: "oauth_authorization_rejected", permanent: true}
}

func (m *Manager) SessionStatus(sessionID string) (SessionStatus, error) {
	m.mu.Lock()
	item := m.sessions[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if item == nil {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	m.expireIfNeeded(item)
	return m.statusFor(item), nil
}

func (m *Manager) CancelSession(sessionID string) error {
	m.mu.Lock()
	item := m.sessions[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if item == nil {
		return &Error{code: "invalid_state", permanent: true}
	}
	item.mu.Lock()
	if item.status == "complete" || item.status == "cancelled" || item.status == "committing" {
		item.mu.Unlock()
		return nil
	}
	item.status = "cancelled"
	item.consumedAt = m.now()
	clearSessionSecretsLocked(item)
	item.mu.Unlock()
	m.stopSession(item)
	return nil
}

func (m *Manager) CredentialStatus(ctx context.Context, credentialID string) (CredentialStatus, error) {
	providers, err := m.store.Providers(ctx)
	if err != nil {
		return CredentialStatus{}, err
	}
	for _, provider := range providers {
		credentials, credentialsErr := m.store.Credentials(ctx, provider.ID)
		if credentialsErr != nil {
			return CredentialStatus{}, credentialsErr
		}
		for _, credential := range credentials {
			if credential.ID != credentialID || (credential.AuthType != "oauth" && credential.AuthType != "service_account") {
				continue
			}
			result := CredentialStatus{CredentialID: credential.ID, ProviderID: provider.ID, Status: credential.Status, TokenType: credential.TokenType}
			if credential.OAuthToken != nil && !credential.OAuthToken.ExpiresAt.IsZero() {
				expires := credential.OAuthToken.ExpiresAt
				result.ExpiresAt = &expires
			}
			if result.ExpiresAt == nil {
				if raw := stringValue(credential.Metadata["vertex_access_expires_at"]); raw != "" {
					if expires, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
						result.ExpiresAt = &expires
					}
				}
			}
			return result, nil
		}
	}
	return CredentialStatus{}, errors.New("credential not found")
}

func (m *Manager) EnsureValid(ctx context.Context, provider store.Provider, credential store.Credential, force bool) (store.Credential, error) {
	switch {
	case provider.Type == "copilot" && credential.AuthType == "oauth":
		return m.ensureCopilotCredential(ctx, provider, credential, force)
	case credential.AuthType == "service_account" && (provider.Type == "vertex" || provider.Type == "vertex-partner"):
		return m.ensureVertexServiceAccount(ctx, credential, force)
	case credential.AuthType != "oauth":
		return credential, nil
	}
	return m.ensureOAuthCredential(ctx, provider, credential, force)
}

// ensureOAuthCredential validates an ordinary OAuth credential and coordinates
// refreshes so concurrent requests share one in-flight exchange. Provider
// adapters such as Copilot call this helper before applying their own token
// transformation; keeping the generic path separate avoids recursive dispatch.
func (m *Manager) ensureOAuthCredential(ctx context.Context, provider store.Provider, credential store.Credential, force bool) (store.Credential, error) {
	token := credential.OAuthToken
	if token == nil {
		token = &store.OAuthToken{AccessToken: credential.Secret, TokenType: credential.TokenType}
	}
	if token.AccessToken == "" {
		_ = m.store.MarkCredentialAuthRequired(ctx, credential.ID, "authorization_required")
		return credential, &Error{code: "authorization_required", permanent: true}
	}
	window := refreshWindow(provider.OAuth)
	if !force && (token.ExpiresAt.IsZero() || token.ExpiresAt.After(m.now().Add(window))) {
		credential.Secret = token.AccessToken
		credential.TokenType = token.TokenType
		credential.OAuthToken = token
		return credential, nil
	}
	if token.RefreshToken == "" {
		_ = m.store.MarkCredentialAuthRequired(ctx, credential.ID, "authorization_required")
		return credential, &Error{code: "authorization_required", permanent: true}
	}

	m.mu.Lock()
	if existing := m.refresh[credential.ID]; existing != nil {
		m.mu.Unlock()
		select {
		case <-existing.done:
			return existing.credential, existing.err
		case <-ctx.Done():
			return credential, ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	m.refresh[credential.ID] = call
	m.mu.Unlock()

	updated, err := m.performRefresh(ctx, provider, credential, *token)
	m.mu.Lock()
	call.credential, call.err = updated, err
	delete(m.refresh, credential.ID)
	close(call.done)
	m.mu.Unlock()
	return updated, err
}

func (m *Manager) ForceRefreshCredential(ctx context.Context, credentialID string) (CredentialStatus, error) {
	credential, err := m.store.CredentialByID(ctx, credentialID)
	if err != nil {
		return CredentialStatus{}, err
	}
	if credential.AuthType != "oauth" && credential.AuthType != "service_account" {
		return CredentialStatus{}, errors.New("only oauth or service account credentials support token refresh")
	}
	provider, err := m.store.Provider(ctx, credential.ProviderID)
	if err != nil {
		return CredentialStatus{}, err
	}
	if credential.AuthType == "service_account" && provider.Type != "vertex" && provider.Type != "vertex-partner" {
		return CredentialStatus{}, errors.New("service account refresh is only supported for Vertex providers")
	}
	if _, err = m.EnsureValid(ctx, *provider, credential, true); err != nil {
		status, statusErr := m.CredentialStatus(ctx, credentialID)
		if statusErr != nil {
			return CredentialStatus{}, err
		}
		return status, err
	}
	return m.CredentialStatus(ctx, credentialID)
}

func (m *Manager) performRefresh(ctx context.Context, provider store.Provider, credential store.Credential, old store.OAuthToken) (store.Credential, error) {
	oauthConfig, err := m.resolveOAuthConfig(ctx, provider)
	if err != nil {
		return credential, err
	}
	token, err := m.exchangeRefresh(ctx, *oauthConfig, old.RefreshToken)
	if err != nil {
		if IsPermanent(err) {
			_ = m.store.MarkCredentialAuthRequired(ctx, credential.ID, Code(err))
		} else {
			_ = m.store.SetCooldown(ctx, credential.ID, Code(err), "OAuth refresh temporarily unavailable", m.now().Add(30*time.Second))
		}
		return credential, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = old.RefreshToken
	}
	mergeTokenExtra(&token, old)
	token, _, err = m.prepareProviderToken(ctx, provider, token, credential.Email)
	if err != nil {
		return credential, err
	}
	if err = m.store.UpdateOAuthToken(ctx, credential.ID, token); err != nil {
		return credential, err
	}
	credential.Secret = token.AccessToken
	credential.TokenType = token.TokenType
	credential.OAuthToken = &token
	credential.Status = "healthy"
	credential.CooldownUntil = time.Time{}
	credential.LastErrorCode = ""
	credential.LastError = ""
	return credential, nil
}

func (m *Manager) refreshExpiring(ctx context.Context) {
	items, err := m.store.OAuthCredentials(ctx)
	if err != nil {
		return
	}
	for _, item := range items {
		if item.Credential.OAuthToken == nil || item.Credential.OAuthToken.ExpiresAt.IsZero() {
			continue
		}
		if item.Credential.OAuthToken.ExpiresAt.After(m.now().Add(refreshWindow(item.Provider.OAuth))) {
			continue
		}
		_, _ = m.EnsureValid(ctx, item.Provider, item.Credential, false)
	}
}

type deviceCodeResponse struct {
	Code            string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	ExpiresIn       time.Duration
}

func (m *Manager) requestDeviceCode(ctx context.Context, cfg config.OAuthConfig) (deviceCodeResponse, error) {
	form := url.Values{}
	for key, value := range cfg.ExtraAuthParams {
		form.Set(key, value)
	}
	form.Set("client_id", m.clientID(cfg))
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	if secret := m.clientSecret(cfg); secret != "" {
		form.Set("client_secret", secret)
	}
	data, status, err := m.postTokenRequest(ctx, cfg.DeviceCodeURL, form, cfg.DeviceRequestFormat)
	if err != nil {
		return deviceCodeResponse{}, err
	}
	if status < 200 || status >= 300 {
		return deviceCodeResponse{}, oauthHTTPError(data, status, false)
	}
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		return deviceCodeResponse{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid device authorization response")}
	}
	code := stringValue(raw["device_code"])
	userCode := stringValue(firstValue(raw, "user_code", "usercode", "verification_code"))
	if strings.EqualFold(cfg.DeviceFlow, "codex") {
		code = stringValue(raw["device_auth_id"])
	}
	verificationURI := stringValue(firstValue(raw, "verification_uri_complete", "verification_uri", "verification_url"))
	if verificationURI == "" {
		verificationURI = cfg.DeviceVerificationURL
	}
	response := deviceCodeResponse{Code: code, UserCode: userCode, VerificationURI: verificationURI, Interval: durationSeconds(raw["interval"], 5), ExpiresIn: durationSeconds(raw["expires_in"], int(defaultSessionTTL/time.Second))}
	if strings.EqualFold(cfg.DeviceFlow, "codex") && response.ExpiresIn <= 0 {
		response.ExpiresIn = 15 * time.Minute
	}
	if response.Code == "" {
		return deviceCodeResponse{}, &Error{code: "oauth_provider_unavailable", err: errors.New("device authorization response did not contain a device code")}
	}
	return response, nil
}

func (m *Manager) pollDevice(item *session) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-item.cancel:
			return
		case <-timer.C:
		}
		m.expireIfNeeded(item)
		item.mu.Lock()
		if item.status != "polling" {
			item.mu.Unlock()
			return
		}
		providerID, deviceCode, deviceUserCode, interval := item.providerID, item.deviceCode, item.deviceUserCode, item.interval
		label, email := item.label, item.email
		item.mu.Unlock()
		provider, err := m.store.Provider(m.rootCtx, providerID)
		if err != nil || provider.OAuth == nil {
			m.failSession(item, "oauth_provider_unavailable")
			return
		}
		oauthConfig, resolveErr := m.resolveOAuthConfig(m.rootCtx, *provider)
		if resolveErr != nil {
			m.failSession(item, Code(resolveErr))
			return
		}
		var token store.OAuthToken
		var pending bool
		if strings.EqualFold(oauthConfig.DeviceFlow, "codex") {
			token, pending, err = m.exchangeCodexDevice(m.rootCtx, *oauthConfig, deviceCode, deviceUserCode)
		} else {
			token, pending, err = m.exchangeDeviceCode(m.rootCtx, *oauthConfig, deviceCode)
		}
		if err == nil {
			token, email, prepareErr := m.prepareProviderToken(m.rootCtx, *provider, token, email)
			if prepareErr != nil {
				m.failSession(item, Code(prepareErr))
				return
			}
			if !m.beginSessionCommit(item, "polling") {
				return
			}
			if err = m.store.SaveOAuthCredential(m.rootCtx, providerID, item.credentialID, label, email, token); err != nil {
				m.failSession(item, "oauth_credential_save_failed")
				return
			}
			m.completeSession(item)
			return
		}
		if !pending {
			m.failSession(item, Code(err))
			return
		}
		if Code(err) == "slow_down" {
			interval += 5 * time.Second
			item.mu.Lock()
			item.interval = interval
			item.mu.Unlock()
		}
		if interval <= 0 {
			interval = 5 * time.Second
		}
		timer.Reset(interval)
	}
}

func (m *Manager) exchangeDeviceCode(ctx context.Context, cfg config.OAuthConfig, deviceCode string) (store.OAuthToken, bool, error) {
	if m.clientID(cfg) == "" {
		return store.OAuthToken{}, false, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	form := url.Values{}
	for key, value := range cfg.ExtraTokenParams {
		form.Set(key, value)
	}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", deviceCode)
	form.Set("client_id", m.clientID(cfg))
	if secret := m.clientSecret(cfg); secret != "" {
		form.Set("client_secret", secret)
	}
	data, status, err := m.postForm(ctx, cfg.TokenURL, form)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	if status < 200 || status >= 300 {
		oauthErr := oauthHTTPError(data, status, true)
		pending := Code(oauthErr) == "authorization_pending" || Code(oauthErr) == "slow_down"
		return store.OAuthToken{}, pending, oauthErr
	}
	var pendingPayload map[string]any
	if json.Unmarshal(data, &pendingPayload) == nil {
		pendingCode := stringValue(pendingPayload["error"])
		if pendingCode == "authorization_pending" || pendingCode == "slow_down" {
			return store.OAuthToken{}, true, &Error{code: pendingCode}
		}
	}
	token, err := parseToken(data, m.now())
	return token, false, err
}

func (m *Manager) exchangeCodexDevice(ctx context.Context, cfg config.OAuthConfig, deviceAuthID, userCode string) (store.OAuthToken, bool, error) {
	if cfg.DeviceTokenURL == "" || userCode == "" {
		return store.OAuthToken{}, false, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	form := url.Values{"device_auth_id": {deviceAuthID}, "user_code": {userCode}}
	data, status, err := m.postTokenRequest(ctx, cfg.DeviceTokenURL, form, cfg.DeviceRequestFormat)
	if err != nil {
		return store.OAuthToken{}, false, err
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return store.OAuthToken{}, true, &Error{code: "authorization_pending"}
	}
	if status < 200 || status >= 300 {
		return store.OAuthToken{}, false, oauthHTTPError(data, status, false)
	}
	var response struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
		CodeChallenge     string `json:"code_challenge"`
	}
	if err = json.Unmarshal(data, &response); err != nil || response.AuthorizationCode == "" || response.CodeVerifier == "" {
		return store.OAuthToken{}, false, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid Codex device token response")}
	}
	redirectURL := cfg.DeviceExchangeRedirectURL
	if redirectURL == "" {
		redirectURL = cfg.RedirectURL
	}
	token, err := m.exchangeCode(ctx, cfg, response.AuthorizationCode, "", response.CodeVerifier, redirectURL)
	return token, false, err
}

func (m *Manager) exchangeCode(ctx context.Context, cfg config.OAuthConfig, code, state, verifier, redirectURL string) (store.OAuthToken, error) {
	if m.clientID(cfg) == "" {
		return store.OAuthToken{}, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	form := url.Values{}
	for key, value := range cfg.ExtraTokenParams {
		form.Set(key, value)
	}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", m.clientID(cfg))
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURL)
	if cfg.IncludeStateInToken {
		form.Set("state", state)
	}
	if secret := m.clientSecret(cfg); secret != "" {
		form.Set("client_secret", secret)
	}
	data, status, err := m.postTokenRequest(ctx, cfg.TokenURL, form, cfg.TokenRequestFormat)
	if err != nil {
		return store.OAuthToken{}, err
	}
	if status < 200 || status >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, status, false)
	}
	return parseToken(data, m.now())
}

func (m *Manager) exchangeRefresh(ctx context.Context, cfg config.OAuthConfig, refreshToken string) (store.OAuthToken, error) {
	if m.clientID(cfg) == "" {
		return store.OAuthToken{}, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	form := url.Values{}
	for key, value := range cfg.ExtraTokenParams {
		form.Set(key, value)
	}
	for key, value := range cfg.ExtraRefreshParams {
		form.Set(key, value)
	}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", m.clientID(cfg))
	if secret := m.clientSecret(cfg); secret != "" {
		form.Set("client_secret", secret)
	}
	data, status, err := m.postTokenRequest(ctx, cfg.TokenURL, form, cfg.TokenRequestFormat)
	if err != nil {
		return store.OAuthToken{}, err
	}
	if status < 200 || status >= 300 {
		return store.OAuthToken{}, oauthHTTPError(data, status, true)
	}
	return parseToken(data, m.now())
}

func (m *Manager) postForm(ctx context.Context, target string, form url.Values) ([]byte, int, error) {
	return m.postTokenRequest(ctx, target, form, "form")
}

func (m *Manager) postTokenRequest(ctx context.Context, target string, form url.Values, requestFormat string) ([]byte, int, error) {
	format := strings.ToLower(strings.TrimSpace(requestFormat))
	var body io.Reader
	contentType := "application/x-www-form-urlencoded"
	if format == "json" {
		payload := make(map[string]string, len(form))
		for key, values := range form {
			if len(values) > 0 {
				payload[key] = values[0]
			}
		}
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return nil, 0, &Error{code: "oauth_configuration_invalid", permanent: true}
		}
		body = strings.NewReader(string(encoded))
		contentType = "application/json"
	} else {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return nil, 0, &Error{code: "oauth_configuration_invalid", permanent: true}
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return nil, 0, &Error{code: "oauth_provider_unavailable", err: err}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return nil, response.StatusCode, &Error{code: "oauth_provider_unavailable"}
	}
	return data, response.StatusCode, nil
}

func oauthHTTPError(data []byte, status int, refresh bool) error {
	code := "oauth_provider_unavailable"
	permanent := false
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		if value := stringValue(raw["error"]); value != "" {
			code = normalizeOAuthErrorCode(value)
		}
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_request", "refresh_token_expired", "refresh_token_reused", "refresh_token_invalidated":
		permanent = refresh
	}
	if refresh && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		permanent = true
	}
	if status >= 500 || status == http.StatusTooManyRequests {
		permanent = false
	}
	if !refresh && code == "invalid_grant" {
		permanent = false
	}
	return &Error{code: code, permanent: permanent}
}

// normalizeOAuthErrorCode prevents provider-controlled error strings from
// becoming status codes, audit fields, or API responses. Only the small set
// of protocol values used by the polling/refresh state machines is exposed.
func normalizeOAuthErrorCode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "authorization_pending", "slow_down", "expired_token", "access_denied", "invalid_grant", "invalid_client", "unauthorized_client", "invalid_request", "refresh_token_expired", "refresh_token_reused", "refresh_token_invalidated", "temporarily_unavailable", "server_error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "oauth_provider_unavailable"
	}
}

func parseToken(data []byte, now time.Time) (store.OAuthToken, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("invalid OAuth token response")}
	}
	access := stringValue(raw["access_token"])
	if access == "" {
		if code := stringValue(raw["error"]); code != "" {
			return store.OAuthToken{}, oauthHTTPError(data, http.StatusBadRequest, true)
		}
		return store.OAuthToken{}, &Error{code: "oauth_provider_unavailable", err: errors.New("OAuth token response did not contain an access token")}
	}
	token := store.OAuthToken{AccessToken: access, RefreshToken: stringValue(raw["refresh_token"]), TokenType: stringValue(raw["token_type"]), Extra: map[string]any{}}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if expires := durationSeconds(raw["expires_in"], 0); expires > 0 {
		token.ExpiresAt = now.Add(expires)
	} else if value := raw["expires_at"]; value != nil {
		switch parsed := value.(type) {
		case string:
			token.ExpiresAt, _ = time.Parse(time.RFC3339, parsed)
		case float64:
			token.ExpiresAt = time.Unix(int64(parsed), 0).UTC()
		}
	}
	for key, value := range raw {
		switch key {
		case "access_token", "refresh_token", "token_type", "expires_in", "expires_at":
		default:
			token.Extra[key] = value
		}
	}
	if idToken := stringValue(raw["id_token"]); idToken != "" {
		if claims := parseJWTClaims(idToken); claims != nil {
			if email := stringValue(claims["email"]); email != "" {
				token.Extra["email"] = email
			}
			if authInfo, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
				if accountID := stringValue(authInfo["chatgpt_account_id"]); accountID != "" {
					token.Extra["account_id"] = accountID
				}
			}
			if subject := stringValue(claims["sub"]); subject != "" {
				token.Extra["subject"] = subject
			}
		}
	}
	if len(token.Extra) == 0 {
		token.Extra = nil
	}
	return token, nil
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}

func authorizationURL(cfg config.OAuthConfig, state, verifier, redirectURL, clientID string) string {
	parsed, err := url.Parse(cfg.AuthorizationURL)
	if err != nil {
		return cfg.AuthorizationURL
	}
	query := parsed.Query()
	for key, value := range cfg.ExtraAuthParams {
		query.Set(key, value)
	}
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURL)
	query.Set("state", state)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	if len(cfg.Scopes) > 0 {
		query.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func pkceVerifier() (string, error) {
	data := make([]byte, 48)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validateRedirectURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("OAuth redirect URL must be an absolute http(s) URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("OAuth redirect URL must be an absolute http(s) URL")
	}
	if scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return errors.New("OAuth redirect URL must use HTTPS for non-loopback hosts")
	}
	return nil
}

func refreshWindow(cfg *config.OAuthConfig) time.Duration {
	if cfg != nil && cfg.RefreshSafetyWindow != "" {
		if value, err := time.ParseDuration(cfg.RefreshSafetyWindow); err == nil && value >= 0 {
			return value
		}
	}
	return defaultRefreshWindow
}

func (m *Manager) clientID(cfg config.OAuthConfig) string {
	if value := config.Env(cfg.ClientIDEnv); value != "" {
		return value
	}
	return strings.TrimSpace(cfg.ClientID)
}
func (m *Manager) clientSecret(cfg config.OAuthConfig) string { return config.Env(cfg.ClientSecretEnv) }

func (m *Manager) statusFor(item *session) SessionStatus {
	item.mu.Lock()
	defer item.mu.Unlock()
	return SessionStatus{SessionID: item.id, ProviderID: item.providerID, CredentialID: item.credentialID, Mode: item.mode, Status: item.status, ExpiresAt: item.expiresAt, ConsumedAt: item.consumedAt, ErrorCode: item.errorCode}
}

func (m *Manager) expireIfNeeded(item *session) {
	expired := false
	item.mu.Lock()
	if m.now().After(item.expiresAt) && sessionCanExpire(item.status) {
		item.status = "expired"
		item.errorCode = "invalid_state"
		clearSessionSecretsLocked(item)
		expired = true
	}
	item.mu.Unlock()
	if expired {
		m.stopSession(item)
	}
}

func sessionCanExpire(status string) bool {
	return !sessionIsTerminal(status) && status != "committing"
}

func sessionIsTerminal(status string) bool {
	return status == "complete" || status == "cancelled" || status == "failed" || status == "expired"
}

// beginSessionCommit atomically claims the final credential write. A
// cancellation that wins before this transition prevents persistence; after
// the transition the database commit is allowed to finish consistently.
func (m *Manager) beginSessionCommit(item *session, expectedStatus string) bool {
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.status != expectedStatus {
		return false
	}
	item.status = "committing"
	return true
}

func (m *Manager) completeSession(item *session) {
	item.mu.Lock()
	item.status = "complete"
	item.errorCode = ""
	clearSessionSecretsLocked(item)
	item.mu.Unlock()
	m.stopSession(item)
}

func (m *Manager) failSession(item *session, code string) {
	item.mu.Lock()
	if item.status == "cancelled" || item.status == "complete" || item.status == "expired" {
		item.mu.Unlock()
		return
	}
	item.status = "failed"
	item.errorCode = code
	clearSessionSecretsLocked(item)
	item.mu.Unlock()
	m.stopSession(item)
}

func clearSessionSecretsLocked(item *session) {
	item.state = ""
	item.verifier = ""
	item.deviceCode = ""
	item.deviceUserCode = ""
}

func (m *Manager) stopSession(item *session) {
	item.mu.Lock()
	server := item.callbackServer
	item.callbackServer = nil
	clearSessionSecretsLocked(item)
	if !item.cancelClosed {
		close(item.cancel)
		item.cancelClosed = true
	}
	item.mu.Unlock()
	if server != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
	}
}

func durationSeconds(value any, fallback int) time.Duration {
	seconds := 0
	switch parsed := value.(type) {
	case float64:
		seconds = int(parsed)
	case int:
		seconds = parsed
	case json.Number:
		seconds, _ = strconv.Atoi(parsed.String())
	case string:
		seconds, _ = strconv.Atoi(parsed)
	}
	if seconds <= 0 {
		seconds = fallback
	}
	return time.Duration(seconds) * time.Second
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// credentialMetadataForPersistence returns a copy suitable for the store's
// metadata column. Proxy-pool IDs are normally decoded into Credential's
// dedicated field, so callers rewriting metadata must add them back or an
// otherwise unrelated token refresh would silently drop egress bindings.
func credentialMetadataForPersistence(credential store.Credential) map[string]any {
	metadata := make(map[string]any, len(credential.Metadata)+1)
	for key, value := range credential.Metadata {
		metadata[key] = value
	}
	if len(credential.ProxyPoolIDs) > 0 {
		metadata["proxy_pool_ids"] = append([]string(nil), credential.ProxyPoolIDs...)
	}
	return metadata
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}
