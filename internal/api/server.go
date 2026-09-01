package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tproxy/tproxy/internal/antigravity"
	"github.com/tproxy/tproxy/internal/auth"
	"github.com/tproxy/tproxy/internal/bridge"
	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/clitools"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/import9router"
	"github.com/tproxy/tproxy/internal/importcliproxy"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
	"github.com/tproxy/tproxy/internal/tunnel"
	"gopkg.in/yaml.v3"
)

type contextKey string

const (
	apiKeyContext     contextKey = "api-key"
	requestLogContext contextKey = "request-log"
)

type requestLogState struct {
	RequestID      string
	ClientAPIKeyID string
	Protocol       string
	PublicModelID  string
	ProviderID     string
	CredentialID   string
	Attempt        int
	ErrorCode      string
}

type Server struct {
	// cfg is swapped wholesale by /api/admin/reload and /api/admin/config/import
	// while other requests are reading it, so it is only ever replaced, never
	// mutated in place. Use currentConfig/setConfig.
	cfg    atomic.Pointer[config.Config]
	store  *store.Store
	router *router.Router
	auth   *auth.Manager
	// managementMu guards the credential state below. It is written by the
	// password-change handler and by config reload, and read by managementAuth
	// on every management request, so unsynchronised access is a race on the
	// gateway's authentication decision.
	managementMu            sync.RWMutex
	managementSecret        string
	managementSecretFromEnv bool
	dashboardPasswordAuth   bool
	allowRemoteMgmt         bool
	allowLanMgmt            bool
	dashboardPasswordCache  *dashboardPasswordCache
	ccFilterNaming          atomic.Bool
	// Idle-connection keepalives; zero disables them.
	streamKeepalive    time.Duration
	nonStreamKeepalive time.Duration
	configPath         string
	limiter            *requestLimiter
	liveUsage          *LiveUsageTracker
	liveLogs           *LiveRequestLogBuffer
	claudeAliases      *bridge.Resolver
	cursorAliases      *bridge.CursorResolver
	modelMappings      *bridge.ModelMappingResolver
	tunnel             *tunnel.Service
	backgroundCancel   context.CancelFunc
	backgroundWG       sync.WaitGroup
}

func NewServer(cfg *config.Config, dataStore *store.Store, requestRouter *router.Router) *Server {
	return NewServerWithAuth(cfg, dataStore, requestRouter, auth.NewManager(dataStore, nil))
}

func NewServerWithAuth(cfg *config.Config, dataStore *store.Store, requestRouter *router.Router, authManager *auth.Manager) *Server {
	if authManager == nil {
		authManager = auth.NewManager(dataStore, nil)
	}
	requestRouter.SetCredentialRefresher(authManager)
	server := &Server{store: dataStore, router: requestRouter, auth: authManager, allowRemoteMgmt: cfg.Server.AllowRemoteManagement, limiter: newRequestLimiter(), liveUsage: NewLiveUsageTracker(), liveLogs: NewLiveRequestLogBuffer(defaultLiveRequestLogLimit), dashboardPasswordCache: newDashboardPasswordCache()}
	server.cfg.Store(cfg)
	server.streamKeepalive = parsePositiveDuration(cfg.Streaming.KeepaliveInterval)
	server.nonStreamKeepalive = parsePositiveDuration(cfg.Streaming.NonStreamKeepaliveInterval)
	server.loadManagementSecret(context.Background())
	server.loadGatewaySettings(context.Background())
	_ = requestRouter.SyncAccountRotationSettings(context.Background())
	server.loadClaudeAliasResolver()
	server.loadCursorAliasResolver()
	server.loadModelMappingResolver()
	return server
}

// currentConfig returns the configuration in force. The returned value is
// shared by concurrent readers and must be treated as immutable; to change a
// field, copy it, edit the copy and hand it to setConfig.
func (s *Server) currentConfig() *config.Config {
	if cfg := s.cfg.Load(); cfg != nil {
		return cfg
	}
	return &config.Config{}
}

func (s *Server) setConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.cfg.Store(cfg)
}

func (s *Server) SetConfigPath(path string) { s.configPath = path }

func (s *Server) StartBackground(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.auth.Start(ctx)
	backgroundCtx, cancel := context.WithCancel(ctx)
	s.backgroundCancel = cancel
	interval, err := time.ParseDuration(s.currentConfig().Retention.CleanupInterval)
	if err != nil || interval <= 0 {
		interval = time.Hour
	}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runRetentionCleanup(backgroundCtx)
			case <-backgroundCtx.Done():
				return
			}
		}
	}()
	s.startAntigravityVersionRefresh(backgroundCtx)
	s.initTunnelService()
	if s.tunnel != nil {
		s.tunnel.StartBackground(backgroundCtx)
	}
}

// startAntigravityVersionRefresh keeps the Antigravity build tproxy announces in
// step with the shipping one. It runs only when an Antigravity provider is
// actually configured, so installations that never touch it make no outbound
// call for it.
func (s *Server) startAntigravityVersionRefresh(ctx context.Context) {
	providers, err := s.store.Providers(ctx)
	if err != nil {
		return
	}
	for _, provider := range providers {
		if provider.Type == "antigravity" && provider.Enabled {
			antigravity.StartVersionRefresh(ctx)
			return
		}
	}
}

func (s *Server) Close() {
	if s.backgroundCancel != nil {
		s.backgroundCancel()
	}
	if s.tunnel != nil {
		s.tunnel.StopBackground()
	}
	s.auth.Close()
	s.backgroundWG.Wait()
}

func (s *Server) runRetentionCleanup(ctx context.Context) {
	if value, err := time.ParseDuration(s.currentConfig().Retention.UsageEvents); err == nil && value > 0 {
		_, _ = s.store.PruneUsage(ctx, time.Now().UTC().Add(-value))
	}
	if value, err := time.ParseDuration(s.currentConfig().Retention.MediaJobs); err == nil && value > 0 {
		_, _ = s.store.PruneMediaJobs(ctx, time.Now().UTC().Add(-value))
	}
	if value, err := time.ParseDuration(s.currentConfig().Retention.AuditEvents); err == nil && value > 0 {
		_, _ = s.store.PruneAuditEvents(ctx, time.Now().UTC().Add(-value))
		_, _ = s.store.PruneConfigVersions(ctx, time.Now().UTC().Add(-value))
	}
	oauthRetention, _ := time.ParseDuration(s.currentConfig().Retention.OAuthSessions)
	s.auth.PurgeExpiredSessions(oauthRetention)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
	})
	mux.Handle("/mcp", s.clientAuth(mcpBridgeHandler(s)))
	mux.Handle("/dashboard/", s.tunnelDashboard(s.dashboard()))
	mux.Handle("/assets/", s.tunnelDashboard(s.dashboard()))
	// OAuth providers cannot attach the management bearer when redirecting the
	// browser. This exact callback route relies on opaque, single-use state; the
	// rest of the admin subtree remains behind management authentication.
	mux.HandleFunc("/api/admin/oauth/callback", s.oauthCallback)
	mux.HandleFunc("/callback", s.browserOAuthCallback)
	mux.Handle("/api/admin/", s.managementAuth(http.HandlerFunc(s.admin)))

	proxy := s.clientAuth(http.HandlerFunc(s.proxyIngress))
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := classifyProxyIngress(r.URL.Path); ok {
			proxy.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	return s.loggingMiddleware(corsMiddleware(root))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	version, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "service": "tproxy", "database": "error", "time": time.Now().UTC()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tproxy", "database": "sqlite", "schema_version": version, "time": time.Now().UTC()})
}

func (s *Server) clientAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, tokenErr := extractClientAPIKey(r)
		var key *store.APIKey
		var err error
		if tokenErr != nil {
			status, code, message := credentialGateStatus(tokenErr)
			writeStreamAwareError(w, r, status, code, message, useClientRequestID(r))
			return
		}
		if token != "" {
			key, err = s.store.AuthenticateAPIKey(r.Context(), token)
			if err != nil {
				writeStreamAwareError(w, r, http.StatusUnauthorized, "invalid_api_key", "invalid client API key", useClientRequestID(r))
				return
			}
			if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok && key != nil {
				state.ClientAPIKeyID = key.ID
			}
		} else if !s.currentConfig().Server.AllowLocalWithoutKey || !security.IsLoopback(r) {
			writeStreamAwareError(w, r, http.StatusUnauthorized, "missing_api_key", "Bearer client API key is required", useClientRequestID(r))
			return
		}
		if err = s.limiter.admitRequest(key, r.URL.Path, s.limitScopes(key)...); err != nil {
			var policyErr *limitError
			if errors.As(err, &policyErr) {
				status := http.StatusTooManyRequests
				if policyErr.Code == "endpoint_forbidden" {
					status = http.StatusForbidden
				}
				writeError(w, status, policyErr.Code, policyErr.Message, useClientRequestID(r))
				return
			}
			writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", err.Error(), useClientRequestID(r))
			return
		}
		if isBodyRequest(r) {
			if maxBytes := s.effectiveLimits(key).MaxInputBytes; maxBytes > 0 && r.Body != nil {
				data, readErr := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
				_ = r.Body.Close()
				if readErr != nil {
					writeError(w, http.StatusBadRequest, "invalid_request", readErr.Error(), useClientRequestID(r))
					return
				}
				if int64(len(data)) > maxBytes {
					writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds API key max_input_bytes", useClientRequestID(r))
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(data))
				r.ContentLength = int64(len(data))
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyContext, key)))
	})
}

func (s *Server) proxyIngress(w http.ResponseWriter, r *http.Request) {
	route, ok := classifyProxyIngress(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found", useClientRequestID(r))
		return
	}
	r = attachIngressRoute(r, route)
	r.URL.Path = route.CanonicalPath

	if route.GeminiAction != "" {
		s.geminiNative(w, r, route.GeminiAction)
		return
	}
	if strings.HasPrefix(route.CanonicalPath, "/v1beta/") || route.CanonicalPath == "/v1beta" {
		s.v1beta(w, r)
		return
	}
	s.v1(w, r)
}

func (s *Server) geminiNative(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	requestedModel := resolveIngressModel(r, stringValue(body["model"]))
	if requestedModel == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "model is required for native Gemini routes", useClientRequestID(r))
		return
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	r.ContentLength = int64(len(encoded))
	s.geminiGenerate(w, r, requestedModel, action)
}

func (s *Server) managementAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.managementClientAllowed(r) {
			writeError(w, http.StatusForbidden, "management_remote_disabled", "remote management is disabled", useClientRequestID(r))
			return
		}
		secret, secretFromEnv, passwordAuth := s.managementCredentials()
		if s.managementRequestViaTunnel(r) && !secretFromEnv {
			writeError(w, http.StatusServiceUnavailable, "tunnel_management_secret_required", "tunnel management requires TPROXY_MANAGEMENT_SECRET", useClientRequestID(r))
			return
		}
		// The bootstrap dashboard password protects only the loopback UI. Every
		// non-loopback management surface must have an operator-provided secret;
		// otherwise a newly installed, publicly reachable service would accept a
		// known default password.
		if !security.IsLoopback(r) && !secretFromEnv {
			writeError(w, http.StatusServiceUnavailable, "management_secret_required", "remote management requires a management secret", useClientRequestID(r))
			return
		}
		token := managementToken(r)
		matched := secret != "" && security.ConstantTimeEqual(token, secret)
		// An environment-managed secret is the sole credential on remote paths;
		// never let the local bootstrap password become a remote fallback. Keep
		// the local dashboard password usable for loopback administration even
		// when an operator also configured a remote secret.
		if !matched && passwordAuth && s.isLocalManagementRequest(r) {
			matched = s.verifyDashboardPassword(r.Context(), token)
		}
		if !matched {
			writeError(w, http.StatusUnauthorized, "invalid_management_secret", "invalid management secret", useClientRequestID(r))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) v1(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/videos/") && r.Method == http.MethodGet {
		s.mediaJobStatus(w, r, strings.TrimPrefix(r.URL.Path, "/v1/videos/"))
		return
	}
	switch r.URL.Path {
	case "/v1/models":
		s.models(w, r)
		return
	case "/v1/models/info":
		s.modelInfo(w, r)
		return
	case "/v1/chat/completions":
		s.chatCompletions(w, r)
		return
	case "/v1/responses":
		s.responses(w, r)
		return
	case "/v1/responses/compact":
		s.responsesCompact(w, r)
		return
	case "/v1/responses/ws":
		s.responsesWebSocket(w, r)
		return
	case "/v1/messages":
		s.messages(w, r)
		return
	case "/v1/messages/count_tokens":
		s.countTokens(w, r)
		return
	case "/v1/embeddings", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/voices", "/v1/videos", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions":
		s.mediaProxy(w, r, r.URL.Path)
		return
	case "/v1/search":
		s.mediaProxy(w, r, r.URL.Path)
		return
	case "/v1/web/fetch":
		s.webFetch(w, r)
		return
	default:
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found", useClientRequestID(r))
	}
}

func (s *Server) v1beta(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1beta/models" {
		s.geminiModels(w, r)
		return
	}
	prefix := "/v1beta/models/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeError(w, 404, "not_found", "endpoint not found", useClientRequestID(r))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	action := "generateContent"
	modelID := path
	if strings.Contains(path, ":") {
		parts := strings.SplitN(path, ":", 2)
		modelID = parts[0]
		action = parts[1]
	}
	s.geminiGenerate(w, r, resolveIngressModel(r, modelID), action)
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	requestID := useClientRequestID(r)
	request := parseOpenAIChat(body, requestID)
	s.applyMappingIngress(r, &request)
	attachIngressMetadata(r, &request)
	s.execute(w, r, request, renderModeOpenAI)
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	requestID := useClientRequestID(r)
	request := parseResponses(body, requestID)
	s.applyMappingIngress(r, &request)
	attachIngressMetadata(r, &request)
	s.execute(w, r, request, renderModeResponses)
}

func (s *Server) responsesCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	body["_compact"] = true
	requestID := useClientRequestID(r)
	request := parseResponses(body, requestID)
	s.applyMappingIngress(r, &request)
	attachIngressMetadata(r, &request)
	s.execute(w, r, request, renderModeResponses)
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	requestID := useClientRequestID(r)
	request := parseClaude(body, requestID)
	s.applyMappingIngress(r, &request)
	attachIngressMetadata(r, &request)
	// Answer Claude Code's housekeeping turns locally before any provider is picked.
	if claudeCLIRequest(r) {
		if text, ok := detectClaudeBypass(request, s.ccFilterNaming.Load()); ok {
			writeClaudeBypass(w, r, request, text)
			return
		}
	}
	s.execute(w, r, request, renderModeClaude)
}

func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	request := parseClaude(body, useClientRequestID(r))
	s.applyMappingIngress(r, &request)
	writeJSON(w, 200, map[string]any{"input_tokens": tokenEstimate(request)})
}

func (s *Server) mediaProxy(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost && !(path == "/v1/audio/voices" && r.Method == http.MethodGet) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required (GET allowed for voices)", useClientRequestID(r))
		return
	}
	const maxMediaBodyBytes int64 = 64 << 20
	bodyReader := io.Reader(http.NoBody)
	if r.Body != nil {
		bodyReader = r.Body
	}
	body, err := io.ReadAll(io.LimitReader(bodyReader, maxMediaBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if int64(len(body)) > maxMediaBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "media request exceeds the 64 MiB gateway limit", useClientRequestID(r))
		return
	}
	requestID := useClientRequestID(r)
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.Protocol = "openai"
	}
	asyncVideo := isAsyncVideoPath(path)
	clientKey, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	clientKeyID := ""
	if clientKey != nil {
		clientKeyID = clientKey.ID
	}
	mediaJobLimit := s.effectiveLimits(clientKey).MediaJobs
	if clientKey != nil && mediaJobLimit > 0 && isAsyncVideoPath(path) {
		active, countErr := s.store.ActiveMediaJobCount(r.Context(), clientKeyID)
		if countErr != nil {
			writeError(w, http.StatusInternalServerError, "media_job_limit_check_failed", countErr.Error(), requestID)
			return
		}
		if active >= mediaJobLimit {
			writeError(w, http.StatusTooManyRequests, "media_job_limit_exceeded", "active media job limit exceeded", requestID)
			return
		}
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if asyncVideo && idempotencyKey != "" {
		if existing, lookupErr := s.store.MediaJobByIdempotency(r.Context(), clientKeyID, idempotencyKey); lookupErr == nil {
			s.writeStoredMediaJob(w, existing)
			return
		} else if !errors.Is(lookupErr, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "media_job_lookup_failed", lookupErr.Error(), requestID)
			return
		}
	}
	if err := s.enforceClientBudget(r.Context(), clientKey); err != nil {
		writeError(w, http.StatusPaymentRequired, "budget_exceeded", err.Error(), requestID)
		return
	}
	contentType := r.Header.Get("Content-Type")
	requestedModel := ""
	if path == "/v1/audio/voices" {
		requestedModel = strings.TrimSpace(r.URL.Query().Get("model"))
	} else {
		requestedModel, err = requestedModelFromBody(body, contentType)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), requestID)
		return
	}
	if requestedModel == "" {
		writeError(w, http.StatusBadRequest, "model_required", "model is required", requestID)
		return
	}
	requestedModel = resolveIngressModel(r, requestedModel)
	pinnedProvider := ""
	disableFallback := false
	if route, ok := ingressRouteFromContext(r); ok {
		pinnedProvider = route.RouteProvider
		disableFallback = route.DisableFallback
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	model, err := s.router.Resolve(r.Context(), requestedModel, key)
	if err != nil {
		writeError(w, http.StatusNotFound, "model_not_found", err.Error(), requestID)
		return
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.PublicModelID = model.ID
	}
	if capability := mediaCapability(path); capability != "" && !modelSupports(model, capability) {
		writeError(w, http.StatusBadRequest, "capability_not_supported", fmt.Sprintf("model %s does not advertise capability %s", model.ID, capability), requestID)
		return
	}
	if contentType == "" {
		contentType = "application/json"
	}
	forwardHeaders := make(http.Header)
	forwardHeaders.Set("X-Request-ID", requestID)
	if idempotencyKey != "" {
		forwardHeaders.Set("Idempotency-Key", idempotencyKey)
	}
	retryNetworkErrors := !requiresIdempotencyForNetworkRetry(path) || idempotencyKey != ""
	team := ""
	if clientKey != nil {
		team = clientKey.Policy.Team
	}
	result, err := s.router.ProxyWithOptions(r.Context(), *model, requestID, path, body, contentType, router.RawProxyOptions{Method: r.Method, Headers: forwardHeaders, RetryNetworkErrors: retryNetworkErrors, DisableFallback: disableFallback, ClientAPIKeyID: clientKeyID, Team: team, PinnedProvider: pinnedProvider})
	if err != nil {
		writeError(w, http.StatusBadGateway, providers.Code(err), err.Error(), requestID)
		return
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.ProviderID = result.Selection.Provider.ID
		state.CredentialID = result.Selection.Credential.ID
		state.Attempt = result.Selection.Attempt
	}
	if asyncVideo {
		if jobErr := s.recordMediaJob(r.Context(), result, *model, clientKeyID, idempotencyKey); jobErr != nil {
			writeError(w, http.StatusInternalServerError, "media_job_persist_failed", jobErr.Error(), requestID)
			return
		}
	}
	if result.Response.ContentType != "" {
		w.Header().Set("Content-Type", result.Response.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(result.Response.Status)
	_, _ = w.Write(result.Response.Body)
}

func isAsyncVideoPath(path string) bool {
	switch path {
	case "/v1/videos", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions":
		return true
	default:
		return false
	}
}

func (s *Server) recordMediaJob(ctx context.Context, result *router.RawResult, model store.PublicModel, clientKeyID, idempotencyKey string) error {
	var payload map[string]any
	if json.Unmarshal(result.Response.Body, &payload) != nil {
		return nil
	}
	upstreamID := stringValue(firstValue(payload, "id", "video_id", "job_id"))
	if upstreamID == "" {
		return nil
	}
	status := stringValue(payload["status"])
	if status == "" {
		if result.Response.Status == http.StatusAccepted {
			status = "queued"
		} else {
			status = "completed"
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.store.SaveMediaJob(ctx, store.MediaJob{ID: upstreamID, Kind: "video", Status: status, PublicModelID: model.ID, ProviderID: result.Selection.Provider.ID, CredentialID: result.Selection.Credential.ID, UpstreamID: upstreamID, ClientAPIKeyID: clientKeyID, IdempotencyKey: idempotencyKey, ResponseJSON: string(encoded), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
}

func (s *Server) writeStoredMediaJob(w http.ResponseWriter, job *store.MediaJob) {
	if job == nil {
		writeError(w, http.StatusNotFound, "media_job_not_found", "media job not found", "")
		return
	}
	var payload map[string]any
	if json.Unmarshal([]byte(job.ResponseJSON), &payload) != nil {
		payload = map[string]any{}
	}
	payload["id"] = job.ID
	payload["status"] = job.Status
	if job.ErrorCode != "" {
		payload["error"] = map[string]any{"code": job.ErrorCode, "message": job.ErrorMessage}
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) mediaJobStatus(w http.ResponseWriter, r *http.Request, id string) {
	id, err := url.PathUnescape(strings.TrimSpace(id))
	if err != nil || id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "video id is required", useClientRequestID(r))
		return
	}
	job, err := s.store.MediaJob(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "media_job_not_found", "video job not found", useClientRequestID(r))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "media_job_lookup_failed", err.Error(), useClientRequestID(r))
		return
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	if job.ClientAPIKeyID != "" && (key == nil || key.ID != job.ClientAPIKeyID) {
		writeError(w, http.StatusNotFound, "media_job_not_found", "video job not found", useClientRequestID(r))
		return
	}
	if !terminalMediaStatus(job.Status) {
		result, refreshErr := s.router.RefreshMediaJob(r.Context(), *job)
		if refreshErr != nil {
			writeError(w, http.StatusBadGateway, providers.Code(refreshErr), refreshErr.Error(), useClientRequestID(r))
			return
		}
		var payload map[string]any
		if json.Unmarshal(result.Response.Body, &payload) == nil {
			status := stringValue(payload["status"])
			if status == "" {
				status = job.Status
			}
			encoded, _ := json.Marshal(payload)
			errorCode, errorMessage := "", ""
			if item, ok := payload["error"].(map[string]any); ok {
				errorCode = stringValue(item["code"])
				errorMessage = stringValue(item["message"])
			}
			if updateErr := s.store.UpdateMediaJob(r.Context(), job.ID, status, string(encoded), errorCode, errorMessage, time.Now().UTC()); updateErr != nil {
				writeError(w, http.StatusInternalServerError, "media_job_update_failed", updateErr.Error(), useClientRequestID(r))
				return
			}
			job.Status = status
			job.ResponseJSON = string(encoded)
			job.ErrorCode = errorCode
			job.ErrorMessage = errorMessage
			job.UpdatedAt = time.Now().UTC()
		}
	}
	s.writeStoredMediaJob(w, job)
}

func terminalMediaStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "succeeded", "failed", "cancelled", "canceled", "expired":
		return true
	default:
		return false
	}
}

func requiresIdempotencyForNetworkRetry(path string) bool {
	switch path {
	case "/v1/images/generations", "/v1/images/edits", "/v1/videos", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions":
		return true
	default:
		return false
	}
}

func mediaCapability(path string) string {
	switch {
	case path == "/v1/embeddings":
		return "embedding"
	case strings.HasPrefix(path, "/v1/images/"):
		return "image-output"
	case path == "/v1/audio/speech":
		return "tts"
	case path == "/v1/audio/voices":
		return "tts"
	case path == "/v1/audio/transcriptions":
		return "stt"
	case strings.HasPrefix(path, "/v1/videos"):
		return "video-output"
	case path == "/v1/search":
		return "web-search"
	default:
		return ""
	}
}

func modelSupports(model *store.PublicModel, capability string) bool {
	if len(model.Capabilities) == 0 {
		return true
	}
	for _, item := range model.Capabilities {
		if item == capability || item == "*" {
			return true
		}
	}
	return false
}

func modelEndpointKind(model store.PublicModel) string {
	for _, item := range model.Capabilities {
		switch item {
		case "image-output":
			return "image"
		case "video-output":
			return "video"
		case "tts":
			return "tts"
		case "stt":
			return "stt"
		case "embedding":
			return "embedding"
		case "web-search":
			return "web_search"
		case "web-fetch":
			return "web_fetch"
		}
	}
	return "llm"
}

func attachClientPolicyMetadata(request *canonical.Request, key *store.APIKey) {
	if request == nil || key == nil {
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]any{}
	}
	request.Metadata["client_api_key_id"] = key.ID
	if key.Policy.Team != "" {
		request.Metadata["team"] = key.Policy.Team
	}
	if len(key.Policy.Tags) > 0 {
		request.Metadata["tags"] = key.Policy.Tags
	}
}

func (s *Server) enforceClientBudget(ctx context.Context, key *store.APIKey) error {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if s.currentConfig().Limits.BudgetUSDPerDay > 0 {
		used, err := s.store.TotalEstimatedCostSince(ctx, start)
		if err != nil {
			return fmt.Errorf("check global budget: %w", err)
		}
		if used >= s.currentConfig().Limits.BudgetUSDPerDay {
			return fmt.Errorf("global daily budget of $%.4f has been exhausted", s.currentConfig().Limits.BudgetUSDPerDay)
		}
	}
	if key != nil && key.Policy.Team != "" {
		for _, team := range s.currentConfig().Teams {
			if team.ID != key.Policy.Team || team.Limits.BudgetUSDPerDay <= 0 {
				continue
			}
			used, err := s.store.TeamEstimatedCostSince(ctx, team.ID, start)
			if err != nil {
				return fmt.Errorf("check team budget: %w", err)
			}
			if used >= team.Limits.BudgetUSDPerDay {
				return fmt.Errorf("daily team budget of $%.4f has been exhausted for %s", team.Limits.BudgetUSDPerDay, team.ID)
			}
			break
		}
	}
	if key == nil || key.Policy.Limits.BudgetUSDPerDay <= 0 {
		return nil
	}
	used, err := s.store.EstimatedCostSince(ctx, key.ID, start)
	if err != nil {
		return fmt.Errorf("check API key budget: %w", err)
	}
	if used >= key.Policy.Limits.BudgetUSDPerDay {
		return fmt.Errorf("daily API key budget of $%.4f has been exhausted", key.Policy.Limits.BudgetUSDPerDay)
	}
	return nil
}

func (s *Server) limitScopes(key *store.APIKey) []limitScope {
	scopes := make([]limitScope, 0, 3)
	if s.currentConfig().Limits != (config.LimitPolicy{}) {
		scopes = append(scopes, limitScope{ID: "global", Limits: s.currentConfig().Limits})
	}
	if key != nil && key.Policy.Team != "" {
		for _, team := range s.currentConfig().Teams {
			if team.ID == key.Policy.Team {
				scopes = append(scopes, limitScope{ID: "team:" + team.ID, Limits: team.Limits})
				break
			}
		}
	}
	if key != nil {
		scopes = append(scopes, limitScope{ID: "key:" + key.ID, Limits: key.Policy.Limits})
	}
	return scopes
}

func (s *Server) effectiveLimits(key *store.APIKey) config.LimitPolicy {
	limits := s.currentConfig().Limits
	if key != nil && key.Policy.Team != "" {
		for _, team := range s.currentConfig().Teams {
			if team.ID == key.Policy.Team {
				limits = mergeLimitPolicies(limits, team.Limits)
				break
			}
		}
	}
	if key != nil {
		limits = mergeLimitPolicies(limits, key.Policy.Limits)
	}
	return limits
}

func mergeLimitPolicies(base, overlay config.LimitPolicy) config.LimitPolicy {
	base.RequestsPerMinute = minPositive(base.RequestsPerMinute, overlay.RequestsPerMinute)
	base.ConcurrentStreams = minPositive(base.ConcurrentStreams, overlay.ConcurrentStreams)
	base.MaxInputBytes = minPositiveInt64(base.MaxInputBytes, overlay.MaxInputBytes)
	base.MaxOutputTokens = minPositive(base.MaxOutputTokens, overlay.MaxOutputTokens)
	base.MediaJobs = minPositive(base.MediaJobs, overlay.MediaJobs)
	if overlay.BudgetUSDPerDay > 0 && (base.BudgetUSDPerDay <= 0 || overlay.BudgetUSDPerDay < base.BudgetUSDPerDay) {
		base.BudgetUSDPerDay = overlay.BudgetUSDPerDay
	}
	return base
}

func minPositive(base, overlay int) int {
	if base <= 0 {
		return overlay
	}
	if overlay <= 0 || base < overlay {
		return base
	}
	return overlay
}

func minPositiveInt64(base, overlay int64) int64 {
	if base <= 0 {
		return overlay
	}
	if overlay <= 0 || base < overlay {
		return base
	}
	return overlay
}

func requestedModelFromBody(body []byte, contentType string) (string, error) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", fmt.Errorf("multipart boundary is required")
		}
		reader := multipart.NewReader(strings.NewReader(string(body)), boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			if part.FormName() == "model" {
				data, _ := io.ReadAll(io.LimitReader(part, 4<<10))
				return strings.TrimSpace(string(data)), nil
			}
		}
		return "", nil
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", fmt.Errorf("JSON or multipart body is required")
	}
	return stringValue(payload["model"]), nil
}

func (s *Server) webFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	target := strings.TrimSpace(stringValue(body["url"]))
	if target == "" {
		writeError(w, http.StatusBadRequest, "url_required", "url is required", useClientRequestID(r))
		return
	}
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		writeError(w, http.StatusBadRequest, "unsafe_url", "only absolute http(s) URLs are allowed", useClientRequestID(r))
		return
	}
	if err = rejectPrivateHost(parsed.Hostname()); err != nil {
		writeError(w, http.StatusForbidden, "unsafe_url", err.Error(), useClientRequestID(r))
		return
	}
	client := safeFetchClient()
	upstream, err := client.Get(parsed.String())
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch_failed", err.Error(), useClientRequestID(r))
		return
	}
	defer upstream.Body.Close()
	data, err := io.ReadAll(io.LimitReader(upstream.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch_failed", err.Error(), useClientRequestID(r))
		return
	}
	maxCharacters := intValue(body["max_characters"])
	if maxCharacters <= 0 || maxCharacters > len(data) {
		maxCharacters = len(data)
	}
	writeJSON(w, upstream.StatusCode, map[string]any{"url": parsed.String(), "status": upstream.StatusCode, "content_type": upstream.Header.Get("Content-Type"), "text": string(data[:maxCharacters])})
}

func rejectPrivateHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("private or loopback targets are blocked")
		}
		return nil
	}
	addresses, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	for _, address := range addresses {
		if isPrivateIP(address) {
			return fmt.Errorf("private or loopback targets are blocked")
		}
	}
	return nil
}

func safeFetchClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil {
			if isPrivateIP(ip) {
				return nil, fmt.Errorf("private or loopback targets are blocked")
			}
			return dialer.DialContext(ctx, network, address)
		}
		addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range addresses {
			if isPrivateIP(ip) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, fmt.Errorf("target resolves only to blocked addresses")
	}}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("redirect scheme is not allowed")
		}
		return rejectPrivateHost(req.URL.Hostname())
	}}
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	return ip.IsUnspecified()
}

type renderMode string

const (
	renderModeOpenAI    renderMode = "openai"
	renderModeResponses renderMode = "responses"
	renderModeClaude    renderMode = "claude"
	renderModeGemini    renderMode = "gemini"
)

func (s *Server) execute(w http.ResponseWriter, r *http.Request, request canonical.Request, mode renderMode) {
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	attachClientPolicyMetadata(&request, key)
	if err := applyRequestControls(r, &request); err != nil {
		if strings.Contains(err.Error(), "prompt_injection") {
			writeError(w, http.StatusBadRequest, "prompt_injection_detected", err.Error(), request.RequestID)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_controls", err.Error(), request.RequestID)
		return
	}
	if err := s.enforceClientBudget(r.Context(), key); err != nil {
		writeError(w, http.StatusPaymentRequired, "budget_exceeded", err.Error(), request.RequestID)
		return
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.Protocol = string(request.Source)
		state.PublicModelID = request.PublicModelID
	}
	if maxOutput := s.effectiveLimits(key).MaxOutputTokens; maxOutput > 0 && request.MaxTokens > maxOutput {
		writeError(w, http.StatusBadRequest, "max_output_tokens_exceeded", "requested output tokens exceed API key policy", request.RequestID)
		return
	}
	if request.Stream {
		if err := s.limiter.acquireStream(key, s.limitScopes(key)...); err != nil {
			var policyErr *limitError
			if errors.As(err, &policyErr) {
				writeError(w, http.StatusTooManyRequests, policyErr.Code, policyErr.Message, request.RequestID)
				return
			}
			writeError(w, http.StatusTooManyRequests, "concurrency_limit_exceeded", err.Error(), request.RequestID)
			return
		}
		defer s.limiter.releaseStream(key, s.limitScopes(key)...)
	}
	model, err := s.router.Resolve(r.Context(), request.PublicModelID, key)
	if err != nil {
		status := 404
		if strings.Contains(err.Error(), "forbidden") {
			status = 403
		}
		writeError(w, status, "model_not_found", err.Error(), request.RequestID)
		return
	}
	request.PublicModelID = model.ID
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.PublicModelID = model.ID
	}
	if request.SessionID == "" {
		request.SessionID = sessionIDFromRequest(r)
	}
	liveTracked := false
	defer func() {
		if liveTracked {
			s.liveUsage.End(request.RequestID)
		}
	}()
	s.applyTokenSaver(&request, w, r)
	if request.Stream {
		stream, errStream := s.router.ExecuteStream(r.Context(), *model, request)
		if errStream != nil {
			if selectionProvider := liveProviderFromContext(r); selectionProvider != "" {
				s.liveUsage.RecordError(selectionProvider)
			}
			writeProviderError(w, errStream, request.RequestID)
			return
		}
		s.liveUsage.Begin(request.RequestID, stream.Selection.Provider.ID, model.ID, stream.Selection.Credential.ID, stream.Selection.Credential.Label)
		liveTracked = true
		if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
			state.ProviderID = stream.Selection.Provider.ID
			state.CredentialID = stream.Selection.Credential.ID
			state.Attempt = stream.Selection.Attempt
		}
		streamWriter, stopKeepalive := newKeepaliveWriter(w, s.streamKeepalive)
		defer stopKeepalive()
		w = streamWriter
		switch mode {
		case renderModeClaude:
			writeClaudeStream(w, r, stream.Events, request.RequestID, clientFacingModel(request, model.ID))
		case renderModeResponses:
			writeResponsesStream(w, r, stream.Events, request.RequestID, clientFacingModel(request, model.ID))
		case renderModeOpenAI:
			writeOpenAIStream(w, r, stream.Events, request.RequestID, clientFacingModel(request, model.ID))
		default:
			writeOpenAIStream(w, r, stream.Events, request.RequestID, clientFacingModel(request, model.ID))
		}
		return
	}
	stopNonStreamKeepalive := startNonStreamKeepalive(w, s.nonStreamKeepalive)
	result, errExecute := s.router.Execute(r.Context(), *model, request)
	stopNonStreamKeepalive()
	if errExecute != nil {
		if selectionProvider := liveProviderFromContext(r); selectionProvider != "" {
			s.liveUsage.RecordError(selectionProvider)
		}
		writeProviderError(w, errExecute, request.RequestID)
		return
	}
	s.liveUsage.Begin(request.RequestID, result.Selection.Provider.ID, model.ID, result.Selection.Credential.ID, result.Selection.Credential.Label)
	liveTracked = true
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.ProviderID = result.Selection.Provider.ID
		state.CredentialID = result.Selection.Credential.ID
		state.Attempt = result.Selection.Attempt
	}
	clientModel := clientFacingModel(request, model.ID)
	var payload any
	switch mode {
	case renderModeClaude:
		payload = renderClaude(result.Response, request.RequestID, clientModel)
	case renderModeResponses:
		payload = renderResponses(result.Response, request.RequestID, clientModel)
	default:
		payload = renderOpenAI(result.Response, request.RequestID, clientModel)
	}
	writeCostHeaders(w, result)
	writeJSON(w, 200, payload)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	models, err := s.store.CatalogModels(r.Context())
	if err != nil {
		writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	restrictedCatalog := !apiKeyAllowsAllModels(key)
	data := make([]map[string]any, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if !model.Enabled || !s.store.PublicModelAllowed(key, model.ID) {
			continue
		}
		// An explicit API-key selection is an administrator decision and must
		// remain visible even when the optional global models.dev registry does
		// not know the model yet. Authorization above still fails closed.
		if !restrictedCatalog && !s.router.IsModelCatalogVisible(r.Context(), model) {
			continue
		}
		display := s.router.CatalogDisplayEntry(r.Context(), model)
		if _, exists := seen[display.ID]; exists {
			continue
		}
		seen[display.ID] = struct{}{}
		data = append(data, map[string]any{"id": display.ID, "object": "model", "name": display.Name, "owned_by": "tproxy", "capabilities": model.Capabilities, "limits": model.Limits, "endpoint": modelEndpointKind(model), "created": time.Now().Unix()})
	}
	for _, entry := range s.placeholderModelCatalog() {
		if !s.placeholderModelListed(key, entry) {
			continue
		}
		id, _ := entry["id"].(string)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		data = append(data, entry)
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) modelInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, 400, "invalid_request", "id is required", useClientRequestID(r))
		return
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	if info, ok := s.placeholderModelInfo(id); ok {
		if !s.placeholderModelAllowed(r.Context(), key, info) {
			writeError(w, 404, "model_not_found", "model is not available to this API key", useClientRequestID(r))
			return
		}
		writeJSON(w, 200, info)
		return
	}
	model, err := s.router.Resolve(r.Context(), id, key)
	if err != nil {
		writeError(w, 404, "model_not_found", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"id": model.ID, "name": model.DisplayName, "capabilities": model.Capabilities, "limits": model.Limits, "endpoint": modelEndpointKind(*model), "supported_parameters": []string{"stream", "temperature", "max_tokens", "tools"}})
}

func (s *Server) geminiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	models, err := s.store.CatalogModels(r.Context())
	if err != nil {
		writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	data := make([]map[string]any, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if !model.Enabled || !s.store.PublicModelAllowed(key, model.ID) {
			continue
		}
		if !s.router.IsModelCatalogVisible(r.Context(), model) {
			continue
		}
		display := s.router.CatalogDisplayEntry(r.Context(), model)
		if _, exists := seen[display.ID]; exists {
			continue
		}
		seen[display.ID] = struct{}{}
		data = append(data, map[string]any{"name": "models/" + display.ID, "displayName": display.Name, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"}, "capabilities": model.Capabilities})
	}
	writeJSON(w, 200, map[string]any{"models": data})
}

func (s *Server) geminiGenerate(w http.ResponseWriter, r *http.Request, requestedModel, action string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	body, err := decodeBody(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	requestID := useClientRequestID(r)
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.Protocol = string(canonical.ProtocolGemini)
		state.PublicModelID = requestedModel
	}
	requestedModel = resolveIngressModel(r, requestedModel)
	request := parseGemini(body, requestID, requestedModel)
	request.Stream = action == "streamGenerateContent"
	key, _ := r.Context().Value(apiKeyContext).(*store.APIKey)
	attachClientPolicyMetadata(&request, key)
	attachIngressMetadata(r, &request)
	if err := s.enforceClientBudget(r.Context(), key); err != nil {
		writeError(w, http.StatusPaymentRequired, "budget_exceeded", err.Error(), requestID)
		return
	}
	if maxOutput := s.effectiveLimits(key).MaxOutputTokens; maxOutput > 0 && request.MaxTokens > maxOutput {
		writeError(w, http.StatusBadRequest, "max_output_tokens_exceeded", "requested output tokens exceed API key policy", requestID)
		return
	}
	if request.Stream {
		if err := s.limiter.acquireStream(key, s.limitScopes(key)...); err != nil {
			writeError(w, http.StatusTooManyRequests, "concurrency_limit_exceeded", err.Error(), requestID)
			return
		}
		defer s.limiter.releaseStream(key, s.limitScopes(key)...)
	}
	model, err := s.router.Resolve(r.Context(), requestedModel, key)
	if err != nil {
		writeError(w, 404, "model_not_found", err.Error(), requestID)
		return
	}
	request.PublicModelID = model.ID
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.PublicModelID = model.ID
	}
	if request.Stream {
		stream, err := s.router.ExecuteStream(r.Context(), *model, request)
		if err != nil {
			writeProviderError(w, err, requestID)
			return
		}
		if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
			state.ProviderID = stream.Selection.Provider.ID
			state.CredentialID = stream.Selection.Credential.ID
			state.Attempt = stream.Selection.Attempt
		}
		writeGeminiStream(w, r, stream.Events)
		return
	}
	result, err := s.router.Execute(r.Context(), *model, request)
	if err != nil {
		writeProviderError(w, err, requestID)
		return
	}
	if state, ok := r.Context().Value(requestLogContext).(*requestLogState); ok {
		state.ProviderID = result.Selection.Provider.ID
		state.CredentialID = result.Selection.Credential.ID
		state.Attempt = result.Selection.Attempt
	}
	writeJSON(w, 200, renderGemini(result.Response))
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/admin/tunnel") {
		s.adminTunnel(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/cli-tools") {
		clitools.NewHandler().ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/proxy-pools/") {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/proxy-pools/")
		if strings.HasSuffix(suffix, "/test") {
			s.adminTestProxyPool(w, r, strings.TrimSuffix(suffix, "/test"))
			return
		}
		s.adminProxyPoolItem(w, r, suffix)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/providers/") {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/providers/")
		if strings.HasSuffix(suffix, "/credentials/order") {
			s.adminReorderProviderCredentials(w, r, strings.TrimSuffix(suffix, "/credentials/order"))
			return
		}
		if strings.HasSuffix(suffix, "/cooldowns/reset") {
			s.adminResetProviderCooldowns(w, r, strings.TrimSuffix(suffix, "/cooldowns/reset"))
			return
		}
		if strings.HasSuffix(suffix, "/health") {
			s.adminProviderHealth(w, r, strings.TrimSuffix(suffix, "/health"))
			return
		}
		if strings.HasSuffix(suffix, "/models") {
			s.adminProviderModels(w, r, strings.TrimSuffix(suffix, "/models"))
			return
		}
		if strings.HasSuffix(suffix, "/descriptor") {
			s.adminProviderDescriptor(w, r, strings.TrimSuffix(suffix, "/descriptor"))
			return
		}
		s.adminDeleteProvider(w, r, suffix)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/models/") {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/models/")
		if suffix == "test" {
			s.adminTestModel(w, r)
			return
		}
		s.adminDeleteModel(w, r, suffix)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/credentials/") {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/credentials/")
		if strings.HasSuffix(suffix, "/refresh") {
			s.adminRefreshCredential(w, r, strings.TrimSuffix(suffix, "/refresh"))
			return
		}
		if strings.HasSuffix(suffix, "/health") {
			s.adminCredentialHealth(w, r, strings.TrimSuffix(suffix, "/health"))
			return
		}
		if strings.HasSuffix(suffix, "/quota") {
			s.adminCredentialQuota(w, r, strings.TrimSuffix(suffix, "/quota"))
			return
		}
		if strings.HasSuffix(suffix, "/usage-chart") {
			s.adminCredentialUsageChart(w, r, strings.TrimSuffix(suffix, "/usage-chart"))
			return
		}
		if strings.HasSuffix(suffix, "/codex-reset-credits") {
			s.adminCodexResetCredits(w, r, strings.TrimSuffix(suffix, "/codex-reset-credits"))
			return
		}
		if strings.HasSuffix(suffix, "/clear-cooldown") {
			s.adminClearCredentialCooldown(w, r, strings.TrimSuffix(suffix, "/clear-cooldown"))
			return
		}
		if strings.HasSuffix(suffix, "/models") {
			s.adminCredentialModels(w, r, strings.TrimSuffix(suffix, "/models"))
			return
		}
		if strings.HasSuffix(suffix, "/chat") {
			s.adminCredentialChat(w, r, strings.TrimSuffix(suffix, "/chat"))
			return
		}
		if strings.HasSuffix(suffix, "/logs") {
			s.adminCredentialLogs(w, r, strings.TrimSuffix(suffix, "/logs"))
			return
		}
		s.adminDeleteCredential(w, r, suffix)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/api-keys/") {
		s.adminAPIKeyItem(w, r, strings.TrimPrefix(r.URL.Path, "/api/admin/api-keys/"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/aliases/") {
		s.adminAliasItem(w, r, strings.TrimPrefix(r.URL.Path, "/api/admin/aliases/"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/combos/") {
		s.adminComboItem(w, r, strings.TrimPrefix(r.URL.Path, "/api/admin/combos/"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/topology/") {
		s.adminTopology(w, r, strings.TrimPrefix(r.URL.Path, "/api/admin/topology/"))
		return
	}
	if r.URL.Path == "/api/admin/logs/stream" {
		s.adminLogsStream(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/usage/") {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/admin/usage/")
		switch suffix {
		case "stats":
			s.adminUsageStats(w, r)
		case "chart":
			s.adminUsageChart(w, r)
		case "stream":
			s.adminUsageStream(w, r)
		default:
			writeError(w, 404, "not_found", "admin endpoint not found", useClientRequestID(r))
		}
		return
	}
	switch r.URL.Path {
	case "/api/admin/proxy-pools":
		s.adminProxyPools(w, r)
	case "/api/admin/snapshot":
		snapshot, err := s.store.Snapshot(r.Context())
		if err != nil {
			writeError(w, 500, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		for i := range snapshot.Providers {
			snapshot.Providers[i].Headers = nil
			snapshot.Providers[i].Config = nil
		}
		writeJSON(w, 200, snapshot)
	case "/api/admin/providers":
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeError(w, 405, "method_not_allowed", "POST or PUT required", useClientRequestID(r))
			return
		}
		var providerCfg config.ProviderConfig
		if err := json.NewDecoder(r.Body).Decode(&providerCfg); err != nil {
			writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		if providerCfg.Type == "plugin-http" && !s.currentConfig().Security.PluginsEnabled {
			writeError(w, http.StatusForbidden, "plugins_disabled", "plugin execution is disabled by security policy", useClientRequestID(r))
			return
		}
		if err := s.store.SaveProvider(r.Context(), providerCfg); err != nil {
			writeError(w, 400, "provider_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_id": providerCfg.ID})
	case "/api/admin/models":
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeError(w, 405, "method_not_allowed", "POST or PUT required", useClientRequestID(r))
			return
		}
		var modelCfg config.PublicModelConfig
		if err := json.NewDecoder(r.Body).Decode(&modelCfg); err != nil {
			writeError(w, 400, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		for _, route := range modelCfg.Routes {
			if _, err := s.store.Provider(r.Context(), route.Provider); err != nil {
				writeError(w, 400, "unknown_provider", route.Provider, useClientRequestID(r))
				return
			}
		}
		if err := s.store.SavePublicModel(r.Context(), modelCfg); err != nil {
			writeError(w, 400, "model_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model_id": modelCfg.ID})
	case "/api/admin/aliases":
		s.adminAliases(w, r)
	case "/api/admin/combos":
		s.adminCombos(w, r)
	case "/api/admin/credentials":
		s.adminSaveCredential(w, r)
	case "/api/admin/api-keys":
		s.adminCreateAPIKey(w, r)
	case "/api/admin/api-key-secrets":
		s.adminAPIKeySecrets(w, r)
	case "/api/admin/usage":
		s.adminUsage(w, r)
	case "/api/admin/quota/summary":
		s.adminQuotaSummary(w, r)
	case "/api/admin/quota/credential-usage":
		s.adminQuotaCredentialUsage(w, r)
	case "/api/admin/logs":
		s.adminLogs(w, r)
	case "/api/admin/audit":
		s.adminAudit(w, r)
	case "/api/admin/config/versions":
		s.adminConfigVersions(w, r)
	case "/api/admin/mapping/claude":
		s.adminClaudeMapping(w, r)
	case "/api/admin/mapping/cursor":
		s.adminCursorMapping(w, r)
	case "/api/admin/mapping/models":
		s.adminModelMapping(w, r)
	case "/api/admin/mapping/models/resolve":
		s.adminModelMappingResolve(w, r)
	case "/api/admin/rotation":
		s.adminAccountRotation(w, r)
	case "/api/admin/rotation/reset":
		s.adminAccountRotationReset(w, r)
	case "/api/admin/failover":
		s.adminFailover(w, r)
	case "/api/admin/failover/reset":
		s.adminFailoverReset(w, r)
	case "/api/admin/settings":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
			return
		}
		tokenSaver, err := s.store.TokenSaverSettings(r.Context())
		if err != nil {
			tokenSaver = store.DefaultTokenSaverSettings()
		}
		gateway, err := s.store.GatewaySettings(r.Context())
		if err != nil {
			gateway = store.DefaultGatewaySettings()
		}
		remoteManagement, _ := s.managementScopes()
		writeJSON(w, http.StatusOK, map[string]any{
			"retention":               s.currentConfig().Retention,
			"payload_capture":         false,
			"allow_remote_management": remoteManagement,
			"allow_lan_management":    gateway.AllowLANManagement,
			"public_base_url":         gateway.PublicBaseURL,
			"server_host":             s.currentConfig().Server.Host,
			"server_port":             s.clientFacingPort(),
			"restart_required":        gateway.AllowLANManagement && isLoopbackBindHost(s.currentConfig().Server.Host),
			"lan_ips":                 lanIPsForGateway(gateway.AllowLANManagement),
			"token_saver": map[string]any{
				"enabled":              true,
				"rtk_enabled":          tokenSaver.RTKEnabled,
				"per_request_opt_out":  tokenSaver.PerRequestOptOut,
				"cli_hook_recommended": tokenSaver.CLIHookRecommended,
				"upstream_project":     "https://github.com/rtk-ai/rtk",
			},
		})
	case "/api/admin/settings/token-saver":
		s.adminTokenSaverSettings(w, r)
	case "/api/admin/settings/dashboard-password":
		s.adminDashboardPassword(w, r)
	case "/api/admin/settings/gateway":
		s.adminGatewaySettings(w, r)
	case "/api/admin/version":
		s.adminVersion(w, r)
	case "/api/admin/import/9router":
		s.adminImport9router(w, r)
	case "/api/admin/ninerouter/presets":
		s.adminNinerouterPresets(w, r)
	case "/api/admin/free-tiers":
		s.adminFreeTiers(w, r)
	case "/api/admin/routing/strategies":
		s.adminRoutingStrategies(w, r)
	case "/api/admin/import/cliproxyapi":
		s.adminImportCliproxyAPI(w, r)
	case "/api/admin/config/export":
		s.adminConfigExport(w, r)
	case "/api/admin/config/import":
		s.adminConfigImport(w, r)
	case "/api/admin/reload":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method_not_allowed", "POST required", useClientRequestID(r))
			return
		}
		if s.configPath == "" {
			writeError(w, 409, "reload_unavailable", "server was started without a config path", useClientRequestID(r))
			return
		}
		next, err := config.Load(s.configPath)
		if err != nil {
			writeError(w, 400, "config_invalid", err.Error(), useClientRequestID(r))
			return
		}
		if err = s.store.Seed(r.Context(), next); err != nil {
			writeError(w, 400, "config_seed_failed", err.Error(), useClientRequestID(r))
			return
		}
		_ = s.store.RecordConfigVersion(r.Context(), "reload", next)
		s.setConfig(next)
		s.router.SetAllowUpstreamModels(next.Server.AllowUpstreamModels)
		s.router.ConfigureRouting(next.Routing)
		_ = s.router.SyncAccountRotationSettings(r.Context())
		s.loadManagementSecret(r.Context())
		s.loadGatewaySettings(r.Context())
		s.setRemoteManagement(next.Server.AllowRemoteManagement)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config_path": s.configPath})
	case "/api/admin/oauth/start":
		s.oauthStart(w, r)
	case "/api/admin/oauth/callback":
		s.oauthCallback(w, r)
	case "/api/admin/oauth/status":
		s.oauthStatus(w, r)
	case "/api/admin/oauth/session":
		s.oauthCancel(w, r)
	case "/api/admin/oauth/cursor/auto-import":
		s.adminCursorOAuthAutoImport(w, r)
	case "/api/admin/oauth/cursor/import":
		s.adminCursorOAuthImport(w, r)
	case "/api/admin/oauth/kiro/auto-import":
		s.adminKiroOAuthAutoImport(w, r)
	case "/api/admin/oauth/kiro/import":
		s.adminKiroOAuthImport(w, r)
	case "/api/admin/oauth/kiro/import-cli-proxy":
		s.adminKiroOAuthImportCLIProxy(w, r)
	case "/api/admin/oauth/kiro/api-key":
		s.adminKiroOAuthAPIKey(w, r)
	case "/api/admin/auth/export":
		s.adminAuthExport(w, r)
	case "/api/admin/auth/import":
		s.adminAuthImport(w, r)
	default:
		writeError(w, 404, "not_found", "admin endpoint not found", useClientRequestID(r))
	}
}

func (s *Server) adminProxyPools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.store.Snapshot(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"proxy_pools": snapshot.ProxyPools})
	case http.MethodPost, http.MethodPut:
		var pool config.ProxyPoolConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&pool); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		if err := s.store.SaveProxyPool(r.Context(), pool); err != nil {
			writeError(w, http.StatusBadRequest, "proxy_pool_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"ok": true, "proxy_pool_id": pool.ID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, POST or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminProxyPoolItem(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "proxy pool id is required", useClientRequestID(r))
		return
	}
	pools, err := s.store.ProxyPools(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	var existing *store.ProxyPool
	for index := range pools {
		if pools[index].ID == id {
			existing = &pools[index]
			break
		}
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "proxy_pool_not_found", "proxy pool not found", useClientRequestID(r))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "name": existing.Name, "url": store.RedactProxyURL(existing.URL), "enabled": existing.Enabled, "status": existing.Status, "last_error": existing.LastError})
	case http.MethodPut:
		var update config.ProxyPoolConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&update); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		update.ID = id
		if update.URL == "" {
			update.URL = existing.URL
		}
		if update.Name == "" {
			update.Name = existing.Name
		}
		if update.Enabled == nil {
			enabled := existing.Enabled
			update.Enabled = &enabled
		}
		if err := s.store.SaveProxyPool(r.Context(), update); err != nil {
			writeError(w, http.StatusBadRequest, "proxy_pool_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy_pool_id": id})
	case http.MethodDelete:
		if err := s.store.DeleteProxyPool(r.Context(), id); err != nil {
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeError(w, status, "proxy_pool_delete_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxy_pool_id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, PUT or DELETE required", useClientRequestID(r))
	}
}

func (s *Server) adminTestProxyPool(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost || id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "POST and a proxy pool id are required", useClientRequestID(r))
		return
	}
	pool, err := s.store.ProxyPool(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "proxy_pool_not_found", "proxy pool not found", useClientRequestID(r))
		return
	}
	target := "https://api.ipify.org?format=json"
	if r.Body != nil {
		var body struct {
			TargetURL string `json:"target_url"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		if strings.TrimSpace(body.TargetURL) != "" {
			target = strings.TrimSpace(body.TargetURL)
		}
	}
	parsed, parseErr := url.Parse(target)
	if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "invalid_target", "target_url must be an absolute http(s) URL", useClientRequestID(r))
		return
	}
	client, clientErr := providers.NewProxyHTTPClient(pool.URL, 10*time.Second)
	if clientErr != nil {
		_ = s.store.SetProxyPoolHealth(r.Context(), id, "error", "invalid proxy configuration", time.Now())
		writeError(w, http.StatusBadRequest, "proxy_pool_test_failed", "invalid proxy configuration", useClientRequestID(r))
		return
	}
	started := time.Now()
	response, requestErr := client.Get(target)
	if requestErr != nil {
		_ = s.store.SetProxyPoolHealth(r.Context(), id, "error", "proxy connection failed", time.Now())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "status": 0, "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 1<<20)
	ok := response.StatusCode >= 200 && response.StatusCode < 300
	status := "error"
	if ok {
		status = "healthy"
	}
	message := ""
	if !ok {
		message = fmt.Sprintf("target returned HTTP %d", response.StatusCode)
	}
	_ = s.store.SetProxyPoolHealth(r.Context(), id, status, message, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "status": response.StatusCode, "elapsed_ms": time.Since(started).Milliseconds(), "error": message})
}

func (s *Server) adminDeleteProvider(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	if providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider id is required", useClientRequestID(r))
		return
	}
	if err := s.store.DeleteProvider(r.Context(), providerID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "provider_delete_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_id": providerID})
}

func (s *Server) adminResetProviderCooldowns(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	providerID = strings.Trim(strings.TrimSuffix(providerID, "/"), "/")
	if providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider id is required", useClientRequestID(r))
		return
	}
	cleared, err := s.store.ClearProviderCooldowns(r.Context(), providerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cooldown_reset_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_id": providerID, "credentials_cleared": cleared})
}

func (s *Server) adminProviderHealth(w http.ResponseWriter, r *http.Request, providerID string) {
	if providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider id is required", useClientRequestID(r))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	if r.Method == http.MethodPost {
		if err := s.router.ProviderHealth(r.Context(), providerID); err != nil {
			provider, lookupErr := s.store.Provider(r.Context(), providerID)
			if lookupErr != nil {
				writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
				return
			}
			writeJSON(w, http.StatusOK, providerHealthPayload(r.Context(), s.store, *provider))
			return
		}
	}
	provider, err := s.store.Provider(r.Context(), providerID)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, providerHealthPayload(r.Context(), s.store, *provider))
}

func providerHealthPayload(ctx context.Context, dataStore *store.Store, provider store.Provider) map[string]any {
	payload := map[string]any{
		"ok":              provider.Status == "healthy",
		"provider_id":     provider.ID,
		"status":          provider.Status,
		"last_error":      provider.LastError,
		"last_checked_at": provider.LastChecked,
	}
	credentials, err := dataStore.Credentials(ctx, provider.ID)
	if err != nil {
		return payload
	}
	checked := 0
	healthy := 0
	failed := 0
	for _, credential := range credentials {
		if !credential.Enabled {
			continue
		}
		checked++
		if credential.Status == "healthy" {
			healthy++
			continue
		}
		failed++
	}
	payload["checked"] = checked
	payload["healthy"] = healthy
	payload["failed"] = failed
	return payload
}

func (s *Server) adminCredentialHealth(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	if r.Method == http.MethodPost {
		if err := s.router.CredentialHealth(r.Context(), credentialID); err != nil {
			credential, lookupErr := s.store.CredentialByID(r.Context(), credentialID)
			if lookupErr != nil {
				writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":            false,
				"credential_id": credentialID,
				"provider_id":   credential.ProviderID,
				"status":        credential.Status,
				"last_error":    credential.LastError,
				"error":         err.Error(),
			})
			return
		}
	}
	credential, err := s.store.CredentialByID(r.Context(), credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            credential.Status == "healthy",
		"credential_id": credentialID,
		"provider_id":   credential.ProviderID,
		"status":        credential.Status,
		"last_error":    credential.LastError,
	})
}

func (s *Server) adminProviderModels(w http.ResponseWriter, r *http.Request, providerID string) {
	if providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider id is required", useClientRequestID(r))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	var items []providers.DiscoveredModel
	var err error
	if r.Method == http.MethodPost {
		items, err = s.router.RefreshProviderModels(r.Context(), providerID)
	} else {
		items, err = s.router.DiscoverProviderModels(r.Context(), providerID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider_id": providerID, "data": []providers.DiscoveredModel{}, "error": map[string]any{"code": providers.Code(err), "message": err.Error(), "request_id": useClientRequestID(r)}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider_id": providerID, "data": items})
}

func (s *Server) adminCredentialModels(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	credential, err := s.store.CredentialByID(r.Context(), credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
		return
	}
	provider, err := s.store.Provider(r.Context(), credential.ProviderID)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
		return
	}
	if credential.AuthType == "oauth" || credential.AuthType == "service_account" {
		updated, ensureErr := s.auth.EnsureValid(r.Context(), *provider, credential, false)
		if ensureErr == nil {
			credential = updated
		}
	}
	items, err := s.router.DiscoverCredentialModels(r.Context(), *provider, credential)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"credential_id": credentialID,
			"provider_id":   credential.ProviderID,
			"data":          []providers.DiscoveredModel{},
			"error": map[string]any{
				"code":       providers.Code(err),
				"message":    err.Error(),
				"request_id": useClientRequestID(r),
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"credential_id": credentialID,
		"provider_id":   credential.ProviderID,
		"data":          items,
	})
}

func (s *Server) adminProviderDescriptor(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodGet || providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "GET and a provider id are required", useClientRequestID(r))
		return
	}
	descriptor, err := s.router.ProviderDescriptor(r.Context(), providerID)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, descriptor)
}

func (s *Server) adminTestModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var request struct {
		ProviderID    string   `json:"provider_id"`
		ModelID       string   `json:"model_id"`
		PublicModelID string   `json:"public_model_id"`
		Kind          string   `json:"kind"`
		CredentialID  string   `json:"credential_id"`
		CredentialIDs []string `json:"credential_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if request.PublicModelID != "" {
		result, err := s.router.TestPublicModel(r.Context(), request.PublicModelID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "model_not_found", "model not found", useClientRequestID(r))
				return
			}
			writeError(w, http.StatusBadRequest, "model_test_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":              result.OK,
			"latency_ms":      result.LatencyMS,
			"error":           result.Error,
			"status":          result.Status,
			"public_model_id": request.PublicModelID,
		})
		return
	}
	if request.ProviderID == "" || request.ModelID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider_id and model_id are required", useClientRequestID(r))
		return
	}
	result, credentialID, err := s.router.TestUpstreamModel(
		r.Context(),
		request.ProviderID,
		request.ModelID,
		request.Kind,
		request.CredentialID,
		request.CredentialIDs,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "provider_not_found", "provider or credential not found", useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            false,
			"latency_ms":    result.LatencyMS,
			"error":         err.Error(),
			"status":        result.Status,
			"provider_id":   request.ProviderID,
			"model_id":      request.ModelID,
			"credential_id": credentialID,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            result.OK,
		"latency_ms":    result.LatencyMS,
		"error":         result.Error,
		"status":        result.Status,
		"provider_id":   request.ProviderID,
		"model_id":      request.ModelID,
		"credential_id": credentialID,
	})
}

func (s *Server) adminDeleteModel(w http.ResponseWriter, r *http.Request, modelID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	if modelID == "" || strings.Contains(modelID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "model id is required", useClientRequestID(r))
		return
	}
	if err := s.store.DeletePublicModel(r.Context(), modelID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "model_delete_failed", err.Error(), useClientRequestID(r))
		return
	}
	if s.configPath != "" {
		next, err := config.RemoveModel(s.configPath, modelID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "config_update_failed", err.Error(), useClientRequestID(r))
			return
		}
		if next != nil {
			s.setConfig(next)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model_id": modelID, "config_updated": s.configPath != ""})
}

func (s *Server) adminAliases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ModelAliases(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost, http.MethodPut:
		var request struct {
			Alias         string `json:"alias"`
			PublicModelID string `json:"public_model_id"`
			APIKeyID      string `json:"api_key_id"`
			TeamID        string `json:"team_id"`
			Enabled       *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		enabled := true
		if request.Enabled != nil {
			enabled = *request.Enabled
		}
		item := store.ModelAlias{Alias: request.Alias, PublicModelID: request.PublicModelID, APIKeyID: request.APIKeyID, TeamID: request.TeamID, Enabled: enabled}
		if err := s.store.SaveModelAlias(r.Context(), item); err != nil {
			writeError(w, http.StatusBadRequest, "alias_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alias": item.Alias, "api_key_id": item.APIKeyID, "team_id": item.TeamID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, POST or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminAliasItem(w http.ResponseWriter, r *http.Request, name string) {
	decoded, err := url.PathUnescape(name)
	if err != nil || strings.TrimSpace(decoded) == "" || strings.Contains(decoded, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "alias is required", useClientRequestID(r))
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	apiKeyID := strings.TrimSpace(r.URL.Query().Get("api_key_id"))
	teamID := strings.TrimSpace(r.URL.Query().Get("team_id"))
	if err := s.store.DeleteScopedModelAlias(r.Context(), decoded, apiKeyID, teamID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "alias_delete_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alias": decoded, "api_key_id": apiKeyID, "team_id": teamID})
}

func (s *Server) adminCombos(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.Combos(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost, http.MethodPut:
		var item config.ComboConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&item); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		if err := s.store.SaveCombo(r.Context(), item); err != nil {
			writeError(w, http.StatusBadRequest, "combo_save_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "combo_id": item.ID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, POST or PUT required", useClientRequestID(r))
	}
}

func (s *Server) adminComboItem(w http.ResponseWriter, r *http.Request, id string) {
	decoded, err := url.PathUnescape(id)
	if err != nil || strings.TrimSpace(decoded) == "" || strings.Contains(decoded, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "combo id is required", useClientRequestID(r))
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	if err := s.store.DeleteCombo(r.Context(), decoded); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "combo_delete_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "combo_id": decoded})
}

func (s *Server) adminSaveCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST or PUT required", useClientRequestID(r))
		return
	}
	var request struct {
		ProviderID string                  `json:"provider_id"`
		Credential config.CredentialConfig `json:"credential"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if err := s.store.SaveCredential(r.Context(), request.ProviderID, request.Credential); err != nil {
		writeError(w, http.StatusBadRequest, "credential_save_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": request.Credential.ID})
}

func (s *Server) adminReorderProviderCredentials(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT required", useClientRequestID(r))
		return
	}
	if providerID == "" || strings.Contains(providerID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider id is required", useClientRequestID(r))
		return
	}
	var request struct {
		CredentialIDs []string `json:"credential_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if err := s.store.ReorderCredentials(r.Context(), providerID, request.CredentialIDs); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "credential_reorder_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_id": providerID, "credential_ids": request.CredentialIDs})
}

func (s *Server) adminDeleteCredential(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	if err := s.store.DeleteCredential(r.Context(), credentialID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "credential_delete_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": credentialID})
}

func (s *Server) adminRefreshCredential(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	status, err := s.auth.ForceRefreshCredential(r.Context(), credentialID)
	if err != nil {
		code := "credential_refresh_failed"
		httpStatus := http.StatusBadRequest
		if auth.IsPermanent(err) {
			httpStatus = http.StatusConflict
			code = auth.Code(err)
		}
		writeError(w, httpStatus, code, err.Error(), useClientRequestID(r))
		return
	}
	_ = s.router.SyncProviderHealth(r.Context(), status.ProviderID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": credentialID, "status": status})
}

func (s *Server) adminClearCredentialCooldown(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	if _, err := s.store.CredentialByID(r.Context(), credentialID); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "credential_not_found", err.Error(), useClientRequestID(r))
		return
	}
	if err := s.store.ClearCooldown(r.Context(), credentialID); err != nil {
		writeError(w, http.StatusInternalServerError, "cooldown_clear_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_id": credentialID})
}

func (s *Server) adminAuthExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	data, err := s.store.ExportAuthBundle(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth_export_failed", err.Error(), useClientRequestID(r))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="tproxy-auth-bundle.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) adminAuthImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(store.MaxAuthBundleBytes)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if len(data) > store.MaxAuthBundleBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "auth bundle exceeds the maximum import size", useClientRequestID(r))
		return
	}
	if err = s.store.ImportAuthBundle(r.Context(), data); err != nil {
		writeError(w, http.StatusBadRequest, "auth_import_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) adminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var request struct {
		ID     string                 `json:"id"`
		Name   string                 `json:"name"`
		Models []string               `json:"models"`
		Policy config.ClientKeyPolicy `json:"policy"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if err := validateAPIKeyModelSelection(request.Models); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model_selection", err.Error(), useClientRequestID(r))
		return
	}
	id, key, err := s.store.CreateAPIKey(r.Context(), request.ID, request.Name, request.Models, request.Policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, "api_key_create_failed", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "key": key, "warning": "This key is shown only once"})
}

func (s *Server) adminAPIKeySecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	secrets, err := s.store.MatchAPIKeySecrets(r.Context(), config.APIKeySecretCandidates(s.currentConfig()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
}

func (s *Server) adminAPIKeyItem(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "API key id is required", useClientRequestID(r))
		return
	}
	switch r.Method {
	case http.MethodPut:
		var request struct {
			Name    string                 `json:"name"`
			Models  []string               `json:"models"`
			Enabled bool                   `json:"enabled"`
			Policy  config.ClientKeyPolicy `json:"policy"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
			return
		}
		if err := validateAPIKeyModelSelection(request.Models); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_model_selection", err.Error(), useClientRequestID(r))
			return
		}
		if err := s.store.UpdateAPIKey(r.Context(), id, request.Name, request.Models, request.Enabled, request.Policy); err != nil {
			writeError(w, http.StatusBadRequest, "api_key_update_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	case http.MethodDelete:
		if err := s.store.DeleteAPIKey(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "api_key_delete_failed", err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT or DELETE required", useClientRequestID(r))
	}
}

const (
	maxAPIKeyModels        = 2048
	maxAPIKeyModelIDLength = 256
)

func validateAPIKeyModelSelection(models []string) error {
	if len(models) > maxAPIKeyModels {
		return fmt.Errorf("models cannot contain more than %d entries", maxAPIKeyModels)
	}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			return errors.New("model ids cannot be empty")
		}
		if len(model) > maxAPIKeyModelIDLength {
			return fmt.Errorf("model id cannot exceed %d bytes", maxAPIKeyModelIDLength)
		}
	}
	return nil
}

func (s *Server) adminCredentialUsageChart(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	if _, err := s.store.CredentialByID(r.Context(), credentialID); err != nil {
		status := http.StatusInternalServerError
		code, message := "store_error", err.Error()
		if errors.Is(err, sql.ErrNoRows) {
			status, code, message = http.StatusNotFound, "credential_not_found", "credential not found"
		}
		writeError(w, status, code, message, useClientRequestID(r))
		return
	}
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "week"
	}
	points, err := s.store.CredentialUsageChart(r.Context(), credentialID, period, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) adminCredentialQuota(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	credential, err := s.store.CredentialByID(r.Context(), credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
		return
	}
	provider, err := s.store.Provider(r.Context(), credential.ProviderID)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
		return
	}
	if credential.AuthType == "oauth" || credential.AuthType == "service_account" {
		updated, ensureErr := s.auth.EnsureValid(r.Context(), *provider, credential, false)
		if ensureErr == nil {
			credential = updated
		}
	}
	quota, err := s.router.CredentialQuota(r.Context(), *provider, credential)
	if err != nil {
		writeError(w, http.StatusBadRequest, providers.Code(err), err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, quota)
}

// adminCredentialChat runs one exchange against a single account so an operator
// can confirm it actually answers. It pins the credential rather than routing
// normally: a request that silently failed over to a healthy sibling would
// report the wrong account as working.
func (s *Server) adminCredentialChat(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	if strings.TrimSpace(request.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "model is required", useClientRequestID(r))
		return
	}
	if len(request.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "at least one message is required", useClientRequestID(r))
		return
	}
	credential, err := s.store.CredentialByID(r.Context(), credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
		return
	}
	messages := make([]canonical.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, canonical.Message{Role: role, Content: message.Content})
	}
	result, err := s.router.ChatWithCredential(r.Context(), credential.ProviderID, credentialID, request.Model, messages)
	if err != nil {
		// The upstream refusing is the answer the operator asked for, so it is
		// reported as a result rather than as a transport failure.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            false,
			"error":         err.Error(),
			"status":        providers.Status(err),
			"latency_ms":    result.LatencyMS,
			"credential_id": credentialID,
			"model":         request.Model,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"content":       result.Content,
		"reasoning":     result.Reasoning,
		"model":         result.Model,
		"latency_ms":    result.LatencyMS,
		"usage":         result.Usage,
		"credential_id": credentialID,
	})
}

func (s *Server) adminCodexResetCredits(w http.ResponseWriter, r *http.Request, credentialID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	credentialID = strings.Trim(strings.TrimSuffix(credentialID, "/"), "/")
	if credentialID == "" || strings.Contains(credentialID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential id is required", useClientRequestID(r))
		return
	}
	credential, err := s.store.CredentialByID(r.Context(), credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "credential not found", useClientRequestID(r))
		return
	}
	provider, err := s.store.Provider(r.Context(), credential.ProviderID)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "provider not found", useClientRequestID(r))
		return
	}
	if provider.Type != "codex" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Codex reset credits are only available for Codex credentials", useClientRequestID(r))
		return
	}
	if credential.AuthType == "oauth" || credential.AuthType == "service_account" {
		updated, ensureErr := s.auth.EnsureValid(r.Context(), *provider, credential, false)
		if ensureErr == nil {
			credential = updated
		}
	}
	if r.Method == http.MethodGet {
		credits, fetchErr := s.router.CodexResetCredits(r.Context(), *provider, credential)
		if fetchErr != nil {
			writeError(w, providers.Status(fetchErr), providers.Code(fetchErr), fetchErr.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, credits)
		return
	}
	result, consumeErr := s.router.ConsumeCodexResetCredit(r.Context(), *provider, credential)
	if consumeErr != nil {
		status := providers.Status(consumeErr)
		if status == 0 {
			status = http.StatusBadGateway
		}
		if result.NoCredit {
			writeJSON(w, http.StatusConflict, map[string]any{
				"ok":        false,
				"reset":     false,
				"code":      result.Code,
				"no_credit": true,
				"message":   result.Message,
			})
			return
		}
		writeJSON(w, status, map[string]any{
			"ok":      result.OK,
			"reset":   result.Reset,
			"code":    result.Code,
			"message": consumeErr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) adminQuotaSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	since := startOfDayUTC(time.Now())
	keys, err := s.store.APIKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	type keySummary struct {
		ID              string  `json:"id"`
		Name            string  `json:"name"`
		Enabled         bool    `json:"enabled"`
		RequestsToday   int     `json:"requests_today"`
		CostUSDToday    float64 `json:"cost_usd_today"`
		BudgetUSDPerDay float64 `json:"budget_usd_per_day,omitempty"`
		RequestsPerMin  int     `json:"requests_per_minute,omitempty"`
	}
	items := make([]keySummary, 0, len(keys))
	for _, key := range keys {
		requests, cost, usageErr := s.store.APIKeyUsageSince(r.Context(), key.ID, since)
		if usageErr != nil {
			writeError(w, http.StatusInternalServerError, "store_error", usageErr.Error(), useClientRequestID(r))
			return
		}
		items = append(items, keySummary{
			ID:              key.ID,
			Name:            key.Name,
			Enabled:         key.Enabled,
			RequestsToday:   requests,
			CostUSDToday:    cost,
			BudgetUSDPerDay: key.Policy.Limits.BudgetUSDPerDay,
			RequestsPerMin:  key.Policy.Limits.RequestsPerMinute,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"global_limits": s.currentConfig().Limits,
		"api_keys":      items,
		"day_start":     since.UTC().Format(time.RFC3339),
	})
}

func (s *Server) adminQuotaCredentialUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "all"
	}
	since, err := store.UsagePeriodSince(period, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	usage, err := s.store.CredentialUsageByPeriod(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	if usage == nil {
		usage = map[string]store.CredentialUsageSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period":        period,
		"by_credential": usage,
	})
}

func startOfDayUTC(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			limit = parsed
		}
	}
	offset := 0
	if value := strings.TrimSpace(r.URL.Query().Get("offset")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			offset = parsed
		}
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	items, total, err := s.store.UsageEvents(r.Context(), store.UsageEventsQuery{
		Limit:      limit,
		Offset:     offset,
		ProviderID: providerID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminUsageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "today"
	}
	since, err := store.UsagePeriodSince(period, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	stats, err := s.store.UsageStats(r.Context(), since, s.usageLookupMaps(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	live := s.liveUsage.Snapshot()
	if len(live.ActiveRequests) > 0 {
		stats.ActiveRequests = make([]any, 0, len(live.ActiveRequests))
		for _, item := range live.ActiveRequests {
			stats.ActiveRequests = append(stats.ActiveRequests, item)
		}
	}
	if live.ErrorProvider != "" {
		stats.ErrorProvider = live.ErrorProvider
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) adminUsageChart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "7d"
	}
	if _, err := store.UsagePeriodSince(period, time.Now().UTC()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	points, err := s.store.UsageChart(r.Context(), period, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) usageLookupMaps(ctx context.Context) store.UsageLookupMaps {
	lookups := store.UsageLookupMaps{
		ProviderNames:  map[string]string{},
		CredentialName: map[string]string{},
		APIKeyNames:    map[string]string{},
	}
	snapshot, err := s.store.Snapshot(ctx)
	if err != nil {
		return lookups
	}
	for _, provider := range snapshot.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = provider.ID
		}
		lookups.ProviderNames[provider.ID] = name
		for _, credential := range snapshot.Credentials[provider.ID] {
			label := strings.TrimSpace(credential.Label)
			if label == "" {
				label = strings.TrimSpace(credential.Email)
			}
			if label == "" {
				label = credential.ID
			}
			lookups.CredentialName[credential.ID] = label
		}
	}
	for _, apiKey := range snapshot.APIKeys {
		name := strings.TrimSpace(apiKey.Name)
		if name == "" {
			name = apiKey.ID
		}
		lookups.APIKeyNames[apiKey.ID] = name
	}
	return lookups
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	items, err := s.store.RecentAuditEvents(r.Context(), queryLimit(r, 50))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) adminConfigVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	items, err := s.store.RecentConfigVersions(r.Context(), queryLimit(r, 25))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) adminImport9router(w http.ResponseWriter, r *http.Request) {
	s.adminImportPayload(w, r, func(ctx context.Context, data []byte, dryRun bool) (importOK, error) {
		return import9router.Import(ctx, s.store, data, import9router.Options{DryRun: dryRun})
	})
}

func (s *Server) adminImportCliproxyAPI(w http.ResponseWriter, r *http.Request) {
	s.adminImportPayload(w, r, func(ctx context.Context, data []byte, dryRun bool) (importOK, error) {
		return importcliproxy.Import(ctx, s.store, data, importcliproxy.Options{DryRun: dryRun})
	})
}

type importOK interface {
	GetOK() bool
}

func (s *Server) adminImportPayload(w http.ResponseWriter, r *http.Request, run func(context.Context, []byte, bool) (importOK, error)) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "import_failed", err.Error(), useClientRequestID(r))
		return
	}
	dryRun := strings.EqualFold(r.URL.Query().Get("dry_run"), "true") || r.URL.Query().Get("dry_run") == "1"
	result, err := run(r.Context(), data, dryRun)
	if err != nil {
		writeError(w, http.StatusBadRequest, "import_failed", err.Error(), useClientRequestID(r))
		return
	}
	status := http.StatusOK
	if !result.GetOK() {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) adminConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	exported, err := s.store.ExportConfigWithOAuthTokens(r.Context(), s.currentConfig())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_export_failed", err.Error(), useClientRequestID(r))
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "yaml") || r.URL.Query().Get("format") == "yaml" {
		data, marshalErr := yaml.Marshal(exported)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, "config_export_failed", marshalErr.Error(), useClientRequestID(r))
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", `attachment; filename="tproxy-export.yaml"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	writeJSON(w, http.StatusOK, exported)
}

func (s *Server) adminConfigImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "config_import_failed", err.Error(), useClientRequestID(r))
		return
	}
	var next config.Config
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "yaml") || strings.Contains(contentType, "yml") {
		err = yaml.Unmarshal(data, &next)
	} else {
		err = json.Unmarshal(data, &next)
		if err != nil {
			err = yaml.Unmarshal(data, &next)
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "config_import_failed", "configuration must be valid JSON or YAML", useClientRequestID(r))
		return
	}
	if err = next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "config_invalid", err.Error(), useClientRequestID(r))
		return
	}
	if err = s.store.Seed(r.Context(), &next); err != nil {
		writeError(w, http.StatusBadRequest, "config_import_failed", err.Error(), useClientRequestID(r))
		return
	}
	_ = s.store.RecordConfigVersion(r.Context(), "import", &next)
	s.setConfig(&next)
	s.router.SetAllowUpstreamModels(next.Server.AllowUpstreamModels)
	s.router.ConfigureRouting(next.Routing)
	s.loadManagementSecret(r.Context())
	s.setRemoteManagement(next.Server.AllowRemoteManagement)
	s.loadGatewaySettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported_at": time.Now().UTC(), "database": "sqlite"})
}

func queryLimit(r *http.Request, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required", useClientRequestID(r))
		return
	}
	var request auth.StartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), useClientRequestID(r))
		return
	}
	provider, providerErr := s.store.Provider(r.Context(), request.ProviderID)
	if providerErr == nil && security.IsLoopback(r) {
		// Claude is deliberately excluded: Anthropic only accepts its own
		// registered redirect URIs, so pointing the flow at this gateway's
		// address makes the authorize request fail before consent. Its
		// configured redirect (config.ClaudeRedirectURL) is used instead, and
		// the operator pastes the code the callback page displays.
		if provider.Type != "claude" && request.RedirectURL == "" {
			hasConfiguredRedirect := provider.OAuth != nil && strings.TrimSpace(provider.OAuth.RedirectURL) != ""
			if !hasConfiguredRedirect {
				callbackURL, callbackErr := defaultOAuthCallbackURL(r)
				if callbackErr != nil {
					writeError(w, http.StatusBadRequest, "oauth_configuration_invalid", callbackErr.Error(), useClientRequestID(r))
					return
				}
				request.RedirectURL = callbackURL
			}
		}
	}
	result, err := s.auth.StartAuthorization(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, auth.Code(err), err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required", useClientRequestID(r))
		return
	}
	state, code, providerError := oauthCallbackValues(r)
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if providerError != "" {
		_, err := s.auth.RejectCallback(state, providerError)
		if err != nil {
			writeError(w, http.StatusBadRequest, auth.Code(err), err.Error(), useClientRequestID(r))
			return
		}
		writeError(w, http.StatusBadRequest, "oauth_authorization_rejected", "OAuth authorization was rejected", useClientRequestID(r))
		return
	}
	result, err := s.auth.CompleteCallback(r.Context(), state, code, sessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, auth.Code(err), err.Error(), useClientRequestID(r))
		return
	}
	_ = s.router.SyncProviderHealth(r.Context(), result.ProviderID)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) browserOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	state, code, providerError := oauthCallbackValues(r)
	if providerError != "" {
		_, _ = s.auth.RejectCallback(state, providerError)
		writeBrowserOAuthPage(w, http.StatusBadRequest, "Authorization failed", providerError)
		return
	}
	result, err := s.auth.CompleteCallback(r.Context(), state, code, "")
	if err != nil {
		writeBrowserOAuthPage(w, http.StatusBadRequest, "Authorization failed", err.Error())
		return
	}
	_ = s.router.SyncProviderHealth(r.Context(), result.ProviderID)
	writeBrowserOAuthPage(w, http.StatusOK, "Authorization complete", "The credential is ready. This window will close in a few seconds.")
}

func (s *Server) oauthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required", useClientRequestID(r))
		return
	}
	if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
		result, err := s.auth.SessionStatus(sessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, auth.Code(err), err.Error(), useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if credentialID := strings.TrimSpace(r.URL.Query().Get("credential_id")); credentialID != "" {
		result, err := s.auth.CredentialStatus(r.Context(), credentialID)
		if err != nil {
			writeError(w, http.StatusNotFound, "credential_not_found", "OAuth credential not found", useClientRequestID(r))
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request", "session_id or credential_id is required", useClientRequestID(r))
}

func (s *Server) oauthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE required", useClientRequestID(r))
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" && r.Body != nil {
		var body struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		sessionID = strings.TrimSpace(body.SessionID)
	}
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required", useClientRequestID(r))
		return
	}
	if err := s.auth.CancelSession(sessionID); err != nil {
		writeError(w, http.StatusNotFound, auth.Code(err), err.Error(), useClientRequestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session_id": sessionID})
}

func defaultOAuthCallbackURL(r *http.Request) (string, error) {
	host, err := loopbackCallbackHost(r.Host)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); forwarded != "" {
		// Dynamic callbacks are derived only for loopback callers. Trusting the
		// forwarded scheme here therefore assumes a local reverse proxy.
		if forwarded != "http" && forwarded != "https" {
			return "", errors.New("OAuth callback forwarded scheme must be http or https")
		}
		scheme = forwarded
	}
	return scheme + "://" + host + "/api/admin/oauth/callback", nil
}

func writeBrowserOAuthPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s</title>
</head>
<body>
<h1>%s</h1>
<p>%s</p>
<script>window.setTimeout(function(){window.close()},3000)</script>
</body>
</html>`, htmlEscape(title), htmlEscape(title), htmlEscape(message))
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(value)
}

func loopbackCallbackHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/\\?#@ \t\r\n") {
		return "", errors.New("OAuth callback host is invalid")
	}
	parsed, err := url.Parse("http://" + raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("OAuth callback host is invalid")
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	ip := net.ParseIP(hostname)
	if hostname != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("OAuth callback host must be loopback or explicitly configured")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("OAuth callback port is invalid")
	}
	if port := parsed.Port(); port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return "", errors.New("OAuth callback port is invalid")
		}
	}
	return parsed.Host, nil
}

func oauthCallbackValues(r *http.Request) (state, code, providerError string) {
	if r.Method == http.MethodGet {
		values := r.URL.Query()
		return values.Get("state"), firstNonEmpty(values.Get("code"), values.Get("token")), values.Get("error")
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var body map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body) == nil {
			return stringValue(body["state"]), firstNonEmpty(stringValue(body["code"]), stringValue(body["token"])), stringValue(body["error"])
		}
		return "", "", "invalid_request"
	}
	_ = r.ParseForm()
	return r.Form.Get("state"), firstNonEmpty(r.Form.Get("code"), r.Form.Get("token")), r.Form.Get("error")
}

func corsMiddleware(next http.Handler) http.Handler {
	allowedHeaders := "Authorization, Content-Type, X-Api-Key, X-Request-ID, X-Model, Idempotency-Key, X-TProxy-Token-Saver"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if !trustedCORSOrigin(origin) {
				writeError(w, http.StatusForbidden, "cors_origin_denied", "cross-origin browser access is not allowed", useClientRequestID(r))
				return
			} else if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func trustedCORSOrigin(origin string) bool {
	if origin == "vscode-file://vscode-app" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	connection, buffered, err := hijacker.Hijack()
	if err == nil && w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return connection, buffered, err
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = security.NewID("req_")
			r.Header.Set("X-Request-ID", requestID)
		}
		state := &requestLogState{RequestID: requestID}
		ctx := context.WithValue(r.Context(), requestLogContext, state)
		r = r.WithContext(ctx)
		writer := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		metadata := map[string]any{"remote_addr": r.RemoteAddr}
		state.ErrorCode = writer.Header().Get("X-TProxy-Error-Code")
		s.liveLogs.Push(store.RequestLog{RequestID: state.RequestID, ClientAPIKeyID: state.ClientAPIKeyID, Method: r.Method, Path: r.URL.Path, Protocol: state.Protocol, PublicModelID: state.PublicModelID, ProviderID: state.ProviderID, CredentialID: state.CredentialID, Attempt: state.Attempt, Status: status, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: state.ErrorCode, Metadata: metadata, CreatedAt: time.Now().UTC()})
		isOAuthCallback := r.URL.Path == "/api/admin/oauth/callback"
		if strings.HasPrefix(r.URL.Path, "/api/admin/") && !isOAuthCallback && r.Method != http.MethodGet && r.Method != http.MethodOptions && !adminRequestIsAction(r.URL.Path) {
			_ = s.store.AddAuditEvent(context.Background(), store.AuditEvent{Actor: "management", Action: r.Method + " " + r.URL.Path, ResourceType: "admin", Status: status, Metadata: map[string]any{"request_id": state.RequestID}, CreatedAt: time.Now().UTC()})
			if status < http.StatusBadRequest {
				_ = s.store.RecordConfigVersion(context.Background(), "admin:"+r.Method+" "+r.URL.Path, s.currentConfig())
			}
		}
		log.Printf("request_id=%s method=%s path=%s status=%d duration_ms=%d", state.RequestID, r.Method, r.URL.Path, status, time.Since(started).Milliseconds())
	})
}

func (s *Server) dashboard() http.Handler { return dashboardHandler() }

func serverAddress(cfg *config.Config) string {
	return fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
}

func isNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

var _ = json.Valid
