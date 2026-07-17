# tproxy API

## Client authentication

Gateway requests use a bearer client API key:

```http
Authorization: Bearer <TPROXY_API_KEY>
```

## Model catalog

```http
GET /v1/models
GET /v1/models/info?id=td-coder-pro
```

Only public model IDs are returned. Upstream model IDs remain visible only in the management snapshot.
Model entries include advertised capabilities, limits and an endpoint kind. Disabled models and models outside the authenticated client-key policy are omitted.

## OpenAI-compatible routes

```http
POST /v1/chat/completions
POST /v1/responses
GET  /v1/responses/ws
```

Both streaming and non-streaming requests are supported. `model` accepts a public model ID or alias.

## Claude-compatible routes

```http
POST /v1/messages
POST /v1/messages/count_tokens
```

## Gemini-compatible routes

```http
GET  /v1beta/models
POST /v1beta/models/{public-model}:generateContent
POST /v1beta/models/{public-model}:streamGenerateContent
```

## Multi-purpose routes

Generic OpenAI-compatible provider routes can expose additional capabilities when the selected model has a matching route:

```http
POST /v1/embeddings
POST /v1/images/generations
POST /v1/images/edits
POST /v1/audio/speech
POST /v1/audio/transcriptions
GET  /v1/audio/voices?model=<public-model>
POST /v1/videos/generations
POST /v1/videos/edits
POST /v1/videos/extensions
POST /v1/web/fetch
POST /v1/search
```

The model field accepts a public ID or alias. JSON responses have their top-level `model` field rewritten to the public model ID when `rewrite_response_model` is enabled. `/v1/web/fetch` blocks loopback, private and link-local targets by default. Image/video creation requests are not retried after an ambiguous network failure unless the request includes an `Idempotency-Key`.

Video creation responses are stored in SQLite with client-key ownership. `GET /v1/videos/{id}` polls the originating provider while a job is non-terminal, persists the updated state and continues to rewrite the public model ID. Reusing the same idempotency key returns the stored job instead of creating another upstream request.

`GET /v1/responses/ws` upgrades an authenticated connection. Send a JSON `response.create` frame containing the same fields as `/v1/responses`; the server emits `response.created`, delta events and `response.completed` frames with the public model ID.

For a `tavily` provider, `/v1/search` accepts `query` (or `q`), `max_results` (or `limit`), `search_depth`, `include_answer` and other Tavily-compatible options. Results are normalized with `object: "search.results"` and the configured public model ID.

For an `elevenlabs` provider, `/v1/audio/speech` translates `input`, `voice`, `model` and `response_format` to ElevenLabs text-to-speech, while `/v1/audio/transcriptions` translates the OpenAI multipart `model` field to ElevenLabs `model_id` and returns a normalized JSON transcription.

Route targets may declare `pricing.input_per_million`, `pricing.output_per_million`, `pricing.reasoning_per_million` and a fixed `pricing.request`. The router records the resulting `estimated_cost_usd` in usage events without exposing provider secrets.

## Management routes

Management access is local-only by default. If `TPROXY_MANAGEMENT_SECRET` is configured, pass it as a bearer token. For local development the official default is `tproxy-local-management-secret` (see `.env.example`).

```http
GET  /api/admin/snapshot
GET  /api/admin/proxy-pools
POST /api/admin/proxy-pools
PUT  /api/admin/proxy-pools/{id}
DELETE /api/admin/proxy-pools/{id}
POST /api/admin/proxy-pools/{id}/test
POST /api/admin/providers
PUT  /api/admin/providers
DELETE /api/admin/providers/{id}
POST /api/admin/models
PUT  /api/admin/models
DELETE /api/admin/models/{id}
POST /api/admin/credentials
PUT  /api/admin/credentials
DELETE /api/admin/credentials/{id}
POST /api/admin/api-keys
PUT  /api/admin/api-keys/{id}
DELETE /api/admin/api-keys/{id}
GET  /api/admin/usage?limit=50
GET  /api/admin/logs?limit=50
GET  /api/admin/audit?limit=50
GET  /api/admin/settings
GET  /api/admin/config/export
POST /api/admin/config/import
GET/POST /api/admin/providers/{id}/health
GET/POST /api/admin/providers/{id}/models
GET/POST/PUT /api/admin/aliases
DELETE /api/admin/aliases/{alias}?api_key_id=<optional>&team_id=<optional>
GET/POST/PUT /api/admin/combos
DELETE /api/admin/combos/{id}
POST /api/admin/reload
POST /api/admin/oauth/start
GET  /api/admin/oauth/callback
POST /api/admin/oauth/callback
GET  /api/admin/oauth/status
DELETE /api/admin/oauth/session
```

Alias writes accept either `api_key_id` or `team_id` as an optional scope; the two scopes are mutually exclusive. Resolution precedence is exact public model, API-key alias, team alias, then global alias.

Provider credentials submitted to the management API are encrypted before persistence and are never returned by the snapshot API.

Proxy pools accept `http://`, `https://`, `socks5://`, `socks5h://`, `direct` and `none`. Their URL ciphertext is encrypted in SQLite; snapshots expose only a redacted host. A provider or credential can bind multiple pool IDs, which are rotated with request selection. Deleting a bound pool returns `409`.

Creating a client key returns its plaintext exactly once. Subsequent snapshots expose only the key ID, name, model policy, enabled state and last-use timestamp.
Client-key policies can restrict endpoints, requests per minute, concurrent streams, input bytes, output tokens, active media jobs and daily estimated cost. Limit failures are typed and occur before provider selection, so they never trigger fallback.

Provider health and model discovery use lightweight catalog requests. Discovery results are suggestions only; administrator-defined public models and aliases are never overwritten automatically.

Combos are ordered lists of public models (optionally pinned to a route target). A combo keeps one stable public ID while the router falls through its ordered items.

Configuration export omits decrypted secrets and emits environment placeholders such as `TPROXY_CREDENTIAL_<ID>` and `TPROXY_PROXY_<ID>`. Import accepts JSON or YAML, validates the full configuration first and only then seeds SQLite, leaving the last valid state active on failure.

The optional `plugin-http` adapter is disabled by default. When explicitly enabled, it speaks a canonical HTTP protocol (`/execute`, `/stream`, `/proxy`, `/models`) in a separate process; plugin configuration and credentials are never returned in management snapshots.

### OAuth authorization

Start a browser PKCE flow:

```http
POST /api/admin/oauth/start
Content-Type: application/json

{
  "provider_id": "provider-id",
  "credential_id": "account-id",
  "mode": "browser"
}
```

The response contains an `authorization_url` and opaque `session_id`. For a device flow, use `"mode": "device"`; the response contains only the user code and verification URI, never the provider device code.

The callback validates a single-use state before exchanging the authorization code. OAuth access/refresh tokens and provider-owned token fields are stored together in one encrypted envelope. Expiring tokens refresh in the background or immediately before dispatch; concurrent requests share one refresh operation. A 401/403 triggers at most one forced refresh and safe retry before normal fallback.

Provider types `codex`, `claude` and `antigravity` include browser-PKCE defaults. They start a short-lived loopback callback listener on the provider-registered redirect port, use the provider's required token request encoding, and attach first-party OAuth headers when dispatching requests. `codex` also supports its authorization-code device bootstrap, `kimi` and `xai` use RFC 8628 device authorization, and Antigravity fetches the Google account email plus Cloud Code project ID during enrollment. Antigravity's OAuth client secret is read from `TPROXY_ANTIGRAVITY_CLIENT_SECRET`; it is never stored in YAML or SQLite plaintext. The generic OAuth fields remain overridable for compatible/self-hosted deployments.

### SQLite maintenance

The binary provides offline/online maintenance commands:

```bash
go run ./cmd/tproxy --config config.yaml --backup-database backups/tproxy.db
go run ./cmd/tproxy --config config.yaml --integrity-check
go run ./cmd/tproxy --config config.yaml --restore-database backups/tproxy.db
go run ./cmd/tproxy --config config.yaml --export-auth backups/oauth-auth.json
go run ./cmd/tproxy --config config.yaml --import-auth backups/oauth-auth.json
```

Backups use `VACUUM INTO` so WAL state is included consistently. Restore stages and verifies a temporary copy before replacing the configured database. Restoring encrypted credentials requires the same `TPROXY_MASTER_KEY`.

OAuth auth bundles contain only encrypted ciphertext and require the same master key. Import refuses unknown providers or ciphertext that cannot be decrypted; access and refresh tokens are never printed.

Status responses are redacted:

```http
GET /api/admin/oauth/status?session_id=<session-id>
GET /api/admin/oauth/status?credential_id=<credential-id>
```
