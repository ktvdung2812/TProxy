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

	// Token freshness policy. expires_at alone is not enough: several providers
	// silently invalidate a refresh token that has not been exercised for a few
	// days, and some issue access tokens with no expiry at all, which used to mean
	// they were never refreshed. So every OAuth credential is rotated on a clock of
	// its own.
	//
	//   defaultMaxTokenAge  — the background loop refreshes once a token reaches
	//                         this age, giving a full day of slack before the ceiling.
	//   defaultHardMaxTokenAge — absolute ceiling. If the gateway was off, offline,
	//                         or refreshes kept failing, the request path refreshes
	//                         before the credential is used.
	defaultMaxTokenAge     = 48 * time.Hour
	defaultHardMaxTokenAge = 72 * time.Hour

	// Retry spacing for an age-based refresh that failed, so a provider outage
	// cannot turn the 30s background tick into a hot retry loop.
	ageRefreshRetryInterval = 15 * time.Minute

	// Each refresh is a network round trip. Running them one at a time makes a
	// sweep of many due credentials outlast the tick interval, so refreshes are
	// spread over a small worker pool.
	defaultRefreshWorkers = 16
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
	ProviderID     string `json:"provider_id"`
	CredentialID   string `json:"credential_id"`
	Label          string `json:"label"`
	Email          string `json:"email"`
	Mode           string `json:"mode"`
	RedirectURL    string `json:"redirect_url"`
	KiroRegion     string `json:"kiro_region,omitempty"`
	KiroStartURL   string `json:"kiro_start_url,omitempty"`
	KiroAuthMethod string `json:"kiro_auth_method,omitempty"`
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
	// Freshness of the stored token, independent of ExpiresAt: when it was last
	// rotated and when the age policy will rotate it next.
	RefreshedAt   *time.Time `json:"refreshed_at,omitempty"`
	NextRefreshAt *time.Time `json:"next_refresh_at,omitempty"`
	MaxRefreshAt  *time.Time `json:"max_refresh_at,omitempty"`
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
	mu                 sync.Mutex
	id                 string
	providerID         string
	credentialID       string
	explicitCredential bool
	label              string
	email              string
	mode               string
	state              string
	verifier           string
	redirectURL        string
	deviceCode         string
	deviceUserCode     string
	deviceMeta         map[string]string
	interval           time.Duration
	expiresAt          time.Time
	status             string
	consumedAt         time.Time
	errorCode          string
	cancel             chan struct{}
	cancelClosed       bool
	callbackServer     *http.Server
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
	// ageRefreshAttempt throttles age-based rotation per credential so a failing
	// provider is retried on a slow cadence rather than every background tick.
	ageRefreshAttempt map[string]time.Time
	// refreshWorkerCount overrides the background refresh concurrency; 0 = default.
	refreshWorkerCount int

	backgroundOnce sync.Once
	backgroundWG   sync.WaitGroup
	prewarm        *PrewarmManager
}

func NewManager(dataStore *store.Store, client *http.Client) *Manager {
	client = oauthHTTPClient(client)
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: dataStore, client: client, rootCtx: ctx, cancel: cancel, sessions: make(map[string]*session), refresh: make(map[string]*refreshCall), discovery: make(map[string]discoveryEntry), ageRefreshAttempt: make(map[string]time.Time), now: time.Now, prewarm: NewPrewarmManager()}
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
	if provider.Type == "claude" {
		config.NormalizeClaudeOAuth(&resolved)
	}
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
	if !oauthAllowsMissingClientID(provider.Type, *oauthConfig) && m.clientID(*oauthConfig) == "" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth client ID is unavailable")}
	}
	if oauthConfig.RequireClientSecret && m.clientSecret(*oauthConfig) == "" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth client secret is unavailable")}
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		if provider.Type == "xai" || provider.Type == "kimi" || provider.Type == "qwen" || provider.Type == "qoder" ||
			provider.Type == "kilocode" || provider.Type == "codebuddy-cn" || provider.Type == "kiro" {
			mode = "device"
		} else if isClineProvider(provider.Type) || provider.Type == "kimchi" || provider.Type == "iflow" || provider.Type == "gitlab" || oauthConfig.AuthorizationURL != "" {
			mode = "browser"
		} else {
			mode = "device"
		}
	}
	if mode != "browser" && mode != "device" {
		return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth mode must be browser or device")}
	}
	credentialID := strings.TrimSpace(request.CredentialID)
	explicitCredential := credentialID != ""
	if credentialID == "" {
		credentialID = security.NewID("oauth_cred_")
	}
	sessionID := security.NewID("oauth_session_")
	expiresAt := m.now().Add(defaultSessionTTL)
	item := &session{
		id: sessionID, providerID: provider.ID, credentialID: credentialID, explicitCredential: explicitCredential,
		label: request.Label, email: request.Email, mode: mode, expiresAt: expiresAt, status: "pending",
		interval: 5 * time.Second, cancel: make(chan struct{}),
	}
	response := StartResponse{SessionID: sessionID, ProviderID: provider.ID, CredentialID: credentialID, Mode: mode, ExpiresAt: expiresAt}
	if mode == "browser" {
		if oauthConfig.AuthorizationURL == "" && !usesCustomBrowserOAuth(provider.Type) {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("browser OAuth requires authorization-url")}
		}
		redirectURL := strings.TrimSpace(request.RedirectURL)
		if redirectURL == "" {
			redirectURL = strings.TrimSpace(oauthConfig.RedirectURL)
		}
		if redirectURL == "" {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("OAuth redirect URL is required")}
		}
		if err := validateRedirectURL(redirectURL); err != nil {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: err}
		}
		item.state = security.NewID("oauth_state_")
		item.redirectURL = redirectURL
		item.status = "pending"
		verifier, err := pkceVerifier()
		if err != nil {
			return StartResponse{}, err
		}
		item.verifier = verifier
		switch provider.Type {
		case "cline", "clinepass":
			response.AuthorizationURL = clineAuthorizationURL(redirectURL)
		case "iflow":
			clientID := m.clientID(*oauthConfig)
			if clientID == "" {
				clientID = iflowClientID
			}
			response.AuthorizationURL = iflowAuthorizationURL(redirectURL, item.state, clientID)
		case "kimchi":
			response.AuthorizationURL = kimchiAuthorizationURL(redirectURL, item.state)
		case "gitlab":
			gitlabCfg := gitlabOAuthFromProvider(*provider, m.clientID(*oauthConfig), m.clientSecret(*oauthConfig))
			if gitlabCfg.ClientID == "" {
				return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("GitLab OAuth requires gitlab_client_id in provider config")}
			}
			response.AuthorizationURL = gitlabAuthorizationURL(gitlabCfg, redirectURL, item.state, pkceChallenge(verifier))
		case "claude":
			response.AuthorizationURL = claudeAuthorizationURL(item.state, verifier, redirectURL, m.clientID(*oauthConfig), oauthConfig.Scopes)
		default:
			response.AuthorizationURL = authorizationURL(*oauthConfig, item.state, verifier, redirectURL, m.clientID(*oauthConfig))
		}
	} else if strings.EqualFold(oauthConfig.DeviceFlow, "qoder") {
		flow, flowErr := initiateQoderDeviceFlow()
		if flowErr != nil {
			return StartResponse{}, flowErr
		}
		item.deviceCode = flow.Nonce
		item.verifier = flow.CodeVerifier
		item.deviceUserCode = flow.MachineID
		item.interval = 2 * time.Second
		item.status = "polling"
		response.VerificationURI = flow.VerificationURI
		response.IntervalSeconds = 2
	} else if strings.EqualFold(oauthConfig.DeviceFlow, "kilocode") {
		flow, flowErr := initiateKilocodeDeviceFlow()
		if flowErr != nil {
			return StartResponse{}, flowErr
		}
		item.deviceCode = flow.Code
		item.interval = time.Duration(flow.Interval) * time.Second
		item.status = "polling"
		response.UserCode = flow.Code
		response.VerificationURI = flow.VerificationURI
		response.IntervalSeconds = flow.Interval
	} else if strings.EqualFold(oauthConfig.DeviceFlow, "codebuddy-cn") {
		flow, flowErr := initiateCodebuddyDeviceFlow()
		if flowErr != nil {
			return StartResponse{}, flowErr
		}
		item.deviceCode = flow.State
		item.interval = flow.Interval
		item.status = "polling"
		response.VerificationURI = flow.VerificationURI
		response.IntervalSeconds = int(flow.Interval / time.Second)
	} else if strings.EqualFold(oauthConfig.DeviceFlow, "kiro") {
		region := kiroDefaultRegion
		startURL := kiroDefaultStartURL
		authMethod := "builder-id"
		if value := strings.TrimSpace(request.KiroRegion); value != "" {
			region = value
		} else if provider.Config != nil {
			if value := strings.TrimSpace(stringValue(provider.Config["kiro_region"])); value != "" {
				region = value
			}
		}
		if value := strings.TrimSpace(request.KiroStartURL); value != "" {
			startURL = value
		} else if provider.Config != nil {
			if value := strings.TrimSpace(stringValue(provider.Config["kiro_start_url"])); value != "" {
				startURL = value
			}
		}
		if value := strings.TrimSpace(request.KiroAuthMethod); value != "" {
			authMethod = value
		} else if provider.Config != nil {
			if value := strings.TrimSpace(stringValue(provider.Config["kiro_auth_method"])); value != "" {
				authMethod = value
			}
		}
		flow, flowErr := initiateKiroDeviceFlow(region, startURL, authMethod)
		if flowErr != nil {
			return StartResponse{}, flowErr
		}
		item.deviceCode = flow.DeviceCode
		item.deviceUserCode = flow.UserCode
		item.deviceMeta = kiroDeviceMeta(flow)
		item.interval = flow.Interval
		item.status = "polling"
		response.UserCode = flow.UserCode
		response.VerificationURI = flow.VerificationURI
		response.IntervalSeconds = int(flow.Interval / time.Second)
	} else {
		if oauthConfig.DeviceCodeURL == "" {
			return StartResponse{}, &Error{code: "oauth_configuration_invalid", err: errors.New("device OAuth requires device-code-url")}
		}
		verifier := ""
		if deviceOAuthUsesPKCE(provider.Type, *oauthConfig) {
			generated, pkceErr := pkceVerifier()
			if pkceErr != nil {
				return StartResponse{}, pkceErr
			}
			verifier = generated
			item.verifier = verifier
		}
		device, err := m.requestDeviceCode(ctx, *oauthConfig, verifier)
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
		_, completeErr := m.CompleteCallback(r.Context(), state, code, item.id)
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
		return values.Get("state"), firstNonEmpty(values.Get("code"), values.Get("token")), values.Get("error"), nil
	case http.MethodPost:
		if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			var payload struct {
				State string `json:"state"`
				Code  string `json:"code"`
				Token string `json:"token"`
				Error string `json:"error"`
			}
			if decodeErr := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); decodeErr != nil {
				return "", "", "", decodeErr
			}
			return strings.TrimSpace(payload.State), firstNonEmpty(strings.TrimSpace(payload.Code), strings.TrimSpace(payload.Token)), strings.TrimSpace(payload.Error), nil
		}
		if parseErr := r.ParseForm(); parseErr != nil {
			return "", "", "", parseErr
		}
		return r.PostForm.Get("state"), firstNonEmpty(r.PostForm.Get("code"), r.PostForm.Get("token")), r.PostForm.Get("error"), nil
	default:
		return "", "", "", errors.New("OAuth callback method is unsupported")
	}
}

func (m *Manager) CompleteCallback(ctx context.Context, state, code, sessionID string) (SessionStatus, error) {
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	sessionID = strings.TrimSpace(sessionID)
	if code == "" {
		return SessionStatus{}, &Error{code: "invalid_state", permanent: true}
	}
	var item *session
	if sessionID != "" {
		m.mu.Lock()
		item = m.sessions[sessionID]
		m.mu.Unlock()
	}
	if item == nil && state != "" {
		item = m.sessionForState(state)
	}
	if item == nil {
		item = m.sessionForSinglePendingStatelessBrowser()
	}
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
	providerID, _, verifier, redirectURL, expectedState := item.providerID, item.credentialID, item.verifier, item.redirectURL, item.state
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
	// Claude (and some Anthropic-style flows) return authorization codes as
	// "code#state". Prefer the embedded state for the token exchange body —
	// matching 9router / Claude Code / CLIProxyAPI — while still rejecting a
	// mismatch against the session state when both are present.
	tokenState := expectedState
	if parts := strings.SplitN(code, "#", 2); len(parts) == 2 {
		codeState := strings.TrimSpace(parts[1])
		code = strings.TrimSpace(parts[0])
		if codeState != "" {
			if expectedState != "" && codeState != expectedState {
				m.failSession(item, "invalid_state")
				return m.statusFor(item), &Error{code: "invalid_state", permanent: true}
			}
			tokenState = codeState
		}
	}
	var token store.OAuthToken
	switch provider.Type {
	case "cline", "clinepass":
		token, err = m.exchangeClineCode(ctx, code, redirectURL, oauthConfig.TokenURL)
	case "iflow":
		token, err = m.exchangeIflowCode(ctx, code, redirectURL, m.clientID(*oauthConfig), m.clientSecret(*oauthConfig))
	case "kimchi":
		token, err = m.exchangeKimchiToken(ctx, code)
	case "gitlab":
		gitlabCfg := gitlabOAuthFromProvider(*provider, m.clientID(*oauthConfig), m.clientSecret(*oauthConfig))
		token, err = m.exchangeGitlabCode(ctx, gitlabCfg, code, redirectURL, verifier)
	default:
		token, err = m.exchangeCode(ctx, *oauthConfig, code, tokenState, verifier, redirectURL)
	}
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
	if err = m.saveOAuthCredentialFromLogin(ctx, item, providerID, label, email, token); err != nil {
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

func (m *Manager) sessionForSinglePendingStatelessBrowser() *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *session
	matches := 0
	for _, candidate := range m.sessions {
		candidate.mu.Lock()
		pending := candidate.status == "pending" && candidate.mode == "browser" && !m.now().After(candidate.expiresAt)
		providerID := candidate.providerID
		candidate.mu.Unlock()
		if !pending {
			continue
		}
		provider, err := m.store.Provider(m.rootCtx, providerID)
		if err != nil || !providerAllowsStatelessCallback(provider.Type) {
			continue
		}
		matches++
		found = candidate
	}
	if matches == 1 {
		return found
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
			// Only meaningful when a rotation is actually possible.
			if credential.OAuthToken != nil && credential.OAuthToken.RefreshToken != "" {
				if refreshedAt, known := tokenRefreshedAt(credential); known {
					next := refreshedAt.Add(maxTokenAge(provider.OAuth))
					max := refreshedAt.Add(hardMaxTokenAge(provider.OAuth))
					result.RefreshedAt = &refreshedAt
					result.NextRefreshAt = &next
					result.MaxRefreshAt = &max
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
	expiryOK := token.ExpiresAt.IsZero() || token.ExpiresAt.After(m.now().Add(window))
	// Even a token that has not expired must be rotated once it passes the hard
	// age ceiling, so a credential can never sit on the same access token
	// indefinitely just because the provider reported a distant (or no) expiry.
	stale := tokenOlderThan(credential, hardMaxTokenAge(provider.OAuth), m.now())
	if !force && expiryOK && !stale {
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
	var token store.OAuthToken
	switch provider.Type {
	case "cline", "clinepass":
		token, err = m.refreshClineToken(ctx, old.RefreshToken, oauthConfig.RefreshURL)
	case "codebuddy-cn":
		token, err = m.refreshCodebuddyToken(ctx, old.RefreshToken, *oauthConfig)
	case "iflow":
		token, err = m.refreshIflowToken(ctx, old.RefreshToken, *oauthConfig)
	case "gitlab":
		gitlabCfg := gitlabOAuthFromProvider(provider, m.clientID(*oauthConfig), m.clientSecret(*oauthConfig))
		if old.Extra != nil {
			if baseURL := strings.TrimSpace(stringValue(old.Extra["base_url"])); baseURL != "" {
				gitlabCfg.BaseURL = baseURL
			}
		}
		token, err = m.refreshGitlabToken(ctx, old.RefreshToken, gitlabCfg)
	case "kiro":
		token, err = m.refreshKiroToken(ctx, old)
	default:
		token, err = m.exchangeRefresh(ctx, *oauthConfig, old.RefreshToken)
	}
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
	now := m.now()
	due := make([]refreshJob, 0, len(items))
	for _, item := range items {
		if item.Credential.OAuthToken == nil {
			continue
		}
		token := item.Credential.OAuthToken
		expiring := !token.ExpiresAt.IsZero() &&
			!token.ExpiresAt.After(now.Add(refreshWindow(item.Provider.OAuth)))
		// Age-based rotation covers the two cases expiry cannot: a token with no
		// expires_at at all, and one whose expiry is far enough out that the
		// provider would drop the refresh token for inactivity first.
		aged := tokenOlderThan(item.Credential, maxTokenAge(item.Provider.OAuth), now)
		if !expiring && !aged {
			continue
		}
		if !expiring && aged {
			// A credential in cooldown just failed; retrying it every tick would
			// hammer the provider. Expiry-driven refreshes stay unthrottled because
			// they are already bounded by the token lifetime.
			if item.Credential.CooldownUntil.After(now) {
				continue
			}
			if !m.ageRefreshDue(item.Credential.ID, now) {
				continue
			}
		}
		due = append(due, refreshJob{item: item, force: !expiring && aged})
	}
	m.runRefreshJobs(ctx, due)
}

type refreshJob struct {
	item store.ProviderCredential
	// force is set for age-driven rotations: EnsureValid judges staleness against
	// the hard ceiling, so at the softer background threshold it would decline and
	// the token would only ever rotate at 72h.
	force bool
}

// runRefreshJobs spreads the due refreshes over a bounded worker pool. Doing
// them one at a time made a sweep of many credentials outlast the tick interval,
// which delayed every refresh behind the slowest provider.
func (m *Manager) runRefreshJobs(ctx context.Context, jobs []refreshJob) {
	if len(jobs) == 0 {
		return
	}
	workers := m.refreshWorkers()
	if workers > len(jobs) {
		workers = len(jobs)
	}
	queue := make(chan refreshJob)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for job := range queue {
				_, _ = m.EnsureValid(ctx, job.item.Provider, job.item.Credential, job.force)
			}
		}()
	}
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			close(queue)
			wait.Wait()
			return
		case queue <- job:
		}
	}
	close(queue)
	wait.Wait()
}

func (m *Manager) refreshWorkers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refreshWorkerCount > 0 {
		return m.refreshWorkerCount
	}
	return defaultRefreshWorkers
}

// SetRefreshWorkers overrides the background refresh concurrency. Values below
// one fall back to the default.
func (m *Manager) SetRefreshWorkers(count int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if count < 1 {
		m.refreshWorkerCount = 0
		return
	}
	m.refreshWorkerCount = count
}

// ageRefreshDue throttles repeated age-based refresh attempts for one credential
// and records this attempt when it allows one through.
func (m *Manager) ageRefreshDue(credentialID string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ageRefreshAttempt == nil {
		m.ageRefreshAttempt = make(map[string]time.Time)
	}
	if last, ok := m.ageRefreshAttempt[credentialID]; ok && now.Sub(last) < ageRefreshRetryInterval {
		return false
	}
	m.ageRefreshAttempt[credentialID] = now
	return true
}

type deviceCodeResponse struct {
	Code            string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	ExpiresIn       time.Duration
}

func (m *Manager) requestDeviceCode(ctx context.Context, cfg config.OAuthConfig, verifier string) (deviceCodeResponse, error) {
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
	if verifier != "" {
		form.Set("code_challenge", pkceChallenge(verifier))
		form.Set("code_challenge_method", "S256")
	}
	data, status, err := m.postTokenRequest(ctx, cfg.DeviceCodeURL, form, cfg.DeviceRequestFormat)
	if err != nil {
		return deviceCodeResponse{}, err
	}
	if status < 200 || status >= 300 {
		if looksLikeHTMLResponse(data) {
			return deviceCodeResponse{}, &Error{code: "oauth_provider_unavailable", err: errors.New("OAuth provider returned an HTML challenge page instead of JSON; retry or check network access")}
		}
		return deviceCodeResponse{}, oauthHTTPError(data, status, false)
	}
	var raw map[string]any
	if err = json.Unmarshal(data, &raw); err != nil {
		if looksLikeHTMLResponse(data) {
			return deviceCodeResponse{}, &Error{code: "oauth_provider_unavailable", err: errors.New("OAuth provider returned an HTML challenge page instead of JSON; retry or check network access")}
		}
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
		verifier := item.verifier
		deviceMeta := item.deviceMeta
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
		} else if strings.EqualFold(oauthConfig.DeviceFlow, "qoder") {
			token, pending, err = m.pollQoderDevice(m.rootCtx, deviceCode, verifier, deviceUserCode)
		} else if strings.EqualFold(oauthConfig.DeviceFlow, "kilocode") {
			token, pending, err = m.pollKilocodeDevice(m.rootCtx, deviceCode)
		} else if strings.EqualFold(oauthConfig.DeviceFlow, "codebuddy-cn") {
			token, pending, err = m.pollCodebuddyDevice(m.rootCtx, deviceCode)
		} else if strings.EqualFold(oauthConfig.DeviceFlow, "kiro") {
			token, pending, err = m.pollKiroDevice(m.rootCtx, deviceCode, deviceMeta)
		} else {
			token, pending, err = m.exchangeDeviceCode(m.rootCtx, *oauthConfig, deviceCode, verifier)
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
			if err = m.saveOAuthCredentialFromLogin(m.rootCtx, item, providerID, label, email, token); err != nil {
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

func (m *Manager) exchangeDeviceCode(ctx context.Context, cfg config.OAuthConfig, deviceCode, verifier string) (store.OAuthToken, bool, error) {
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
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
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
	// Claude token responses embed account.email_address (no id_token).
	if email := claudeAccountEmail(raw); email != "" {
		token.Extra["email"] = email
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

// claudeAccountEmail extracts the login email from Anthropic Claude token JSON.
func claudeAccountEmail(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	if account, ok := raw["account"].(map[string]any); ok {
		if email := strings.TrimSpace(stringValue(account["email_address"])); email != "" {
			return email
		}
		if email := strings.TrimSpace(stringValue(account["email"])); email != "" {
			return email
		}
	}
	return ""
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

func deviceOAuthUsesPKCE(providerType string, cfg config.OAuthConfig) bool {
	if cfg.DevicePKCE {
		return true
	}
	return providerType == "qwen"
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

func positiveDuration(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// maxTokenAge is the age at which the background loop rotates a token.
func maxTokenAge(cfg *config.OAuthConfig) time.Duration {
	if cfg == nil {
		return defaultMaxTokenAge
	}
	return positiveDuration(cfg.MaxTokenAge, defaultMaxTokenAge)
}

// hardMaxTokenAge is the ceiling enforced before a credential is used. It can
// never be below maxTokenAge, otherwise the request path would refresh ahead of
// the background loop on every single call.
func hardMaxTokenAge(cfg *config.OAuthConfig) time.Duration {
	soft := maxTokenAge(cfg)
	hard := defaultHardMaxTokenAge
	if cfg != nil {
		hard = positiveDuration(cfg.HardMaxTokenAge, defaultHardMaxTokenAge)
	}
	if hard < soft {
		return soft
	}
	return hard
}

// tokenRefreshedAt reports when the credential's token was last written. Both
// UpdateOAuthToken and the login save stamp last_validated_at, so it doubles as
// "last successful refresh". A credential that predates the column falls back to
// its creation time; when neither is known the token is treated as due, which is
// the safe direction — a redundant refresh costs one request, a skipped one can
// cost the credential.
func tokenRefreshedAt(credential store.Credential) (time.Time, bool) {
	if !credential.LastValidated.IsZero() {
		return credential.LastValidated, true
	}
	if !credential.CreatedAt.IsZero() {
		return credential.CreatedAt, true
	}
	return time.Time{}, false
}

// tokenOlderThan reports whether the credential has gone unrefreshed for at
// least limit. Credentials without a refresh token are always false: rotating is
// impossible, and reporting them as stale would mark healthy long-lived
// credentials as needing re-authorization.
func tokenOlderThan(credential store.Credential, limit time.Duration, now time.Time) bool {
	if credential.OAuthToken == nil || credential.OAuthToken.RefreshToken == "" {
		return false
	}
	refreshedAt, known := tokenRefreshedAt(credential)
	if !known {
		return true
	}
	return now.Sub(refreshedAt) >= limit
}

func (m *Manager) saveOAuthCredentialFromLogin(ctx context.Context, item *session, providerID, label, email string, token store.OAuthToken) error {
	item.mu.Lock()
	candidateID := item.credentialID
	explicit := item.explicitCredential
	item.mu.Unlock()
	credentialID, err := m.store.OAuthCredentialIDForLogin(ctx, providerID, candidateID, email, explicit)
	if err != nil {
		return err
	}
	if existing, loadErr := m.store.CredentialByID(ctx, credentialID); loadErr == nil && existing.OAuthToken != nil {
		mergeTokenExtra(&token, *existing.OAuthToken)
	}
	item.mu.Lock()
	item.credentialID = credentialID
	item.mu.Unlock()
	return m.store.SaveOAuthCredential(ctx, providerID, credentialID, label, email, token)
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
