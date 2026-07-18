# Specification implementation matrix

## Implemented

- Standalone project named `tproxy`.
- Go gateway with embedded React dashboard.
- SQLite schema, migrations, YAML bootstrap and runtime reload.
- Versioned SQLite migrations with WAL-safe backup, restore and integrity-check commands.
- Encrypted provider secrets and hashed client API keys.
- Public models, aliases, route targets and upstream response-name rewriting.
- Ordered cross-provider fallback, credential rotation and cooldown.
- Round-robin/fill-first/weighted-round-robin scheduling, sticky account round-robin (9router-style), and session affinity with TTL.
- OpenAI Chat Completions and Responses, including SSE normalization.
- Authenticated Responses WebSocket transport with public-model rewriting.
- Claude Messages and count-token compatibility.
- Gemini generateContent and streamGenerateContent compatibility.
- OpenAI-compatible, Anthropic-compatible, Gemini and Ollama adapters.
- Generic routing for embeddings, images, audio speech/transcriptions and video endpoints.
- Idempotency-gated network fallback for image/video creation requests.
- Specialized Tavily `/v1/search` adapter with normalized search results.
- Specialized ElevenLabs TTS/STT adapter with OpenAI payload and multipart translation.
- SSRF-protected web fetch.
- Fail-open tool-result token compression with per-request opt-out and saved-token usage metrics.
- Management snapshot plus provider/model update APIs.
- Structured usage attempts and a reusable Go service builder.
- Unit, integration, race and HTTP smoke tests for core routing behavior.
- Generic OAuth browser PKCE and device authorization framework.
- Encrypted OAuth token envelopes with single-use state and redacted management APIs.
- Pre-expiry/background refresh, concurrent refresh deduplication and one-time 401/403 refresh retry.
- Codex Responses and Claude provider profiles, including Codex SSE normalization and Claude OAuth JSON token exchange.
- Codex proprietary device bootstrap and Kimi RFC 8628 device authorization profile.
- xAI OIDC discovery, RFC 8628 device authorization and token refresh profile.
- Antigravity Google OAuth PKCE profile, Cloud Code project bootstrap and Gemini-compatible request/SSE adapter.
- Management CRUD for providers, credentials, public models, client API keys and recent usage.
- Dashboard configuration editors with one-time client-key display and destructive-action confirmation.
- Dashboard OAuth wizard for browser/device enrollment, status polling and session cancellation.
- Encrypted proxy-pool storage, provider/credential bindings, HTTP(S)/SOCKS5 transport and health-test CRUD.
- Per-route pricing and estimated-cost usage records.
- SQLite-backed request logs, audit events, configurable retention cleanup and readiness health reporting.
- API-key/team scoped aliases, endpoint/rate/concurrency/input/media/daily-budget policies with typed pre-routing rejection.
- Provider health checks and lightweight model discovery management APIs.
- Ordered public-model combos with optional route pinning and fallback across combo items.
- Vertex project-scoped Gemini adapter, image/video provider aliases and opt-in HTTP plugin protocol.
- Secret-free JSON/YAML configuration export/import with validation-before-activation.
- Provider concurrent-stream enforcement, global/team/client rate scopes and team daily-cost aggregation.
- WebSocket upgrade request logging with correlation IDs, sensitive provider snapshot redaction and migration-upgrade coverage for every supported SQLite schema version.

## Partially implemented

- Dashboard account rotation settings per provider on `/dashboard/providers/:id` with strategy overrides and credential rotation stats.
- Image/video capability-specific upstreams use specialized provider aliases and durable SQLite video status polling; provider-specific image/video APIs beyond the canonical HTTP contract remain adapter extensions.
- Search supports Tavily; additional search providers and provider-specific ranking/citation options remain.
- Usage records contain token counts and estimated route cost when pricing is configured; global/team/client daily budgets and client-key resource limits are enforced before fallback.
- Raw upstream model access is optional and maps back to the owning public model.

## Staged extensions

- Advanced dashboard drag/drop route editing, provider-specific media controls and richer health charts.
- Optional plugin store/distribution and gRPC transport (HTTP plugin protocol is implemented and disabled by default).
- Shared provider media-job limits and richer global/team media accounting beyond the implemented request, stream and budget policies.
