# tproxy

`tproxy` is a self-hosted, multi-provider AI gateway built from the specifications in `../specs`.

The gateway exposes stable public model IDs, rewrites upstream model names in responses, rotates credentials, and falls back across providers without requiring client reconfiguration.

## Development

One command starts the Go gateway and the Vite dashboard dev server:

```bash
cp config.example.yaml config.yaml
cp .env.example .env.run
# Edit .env.run: set TPROXY_MASTER_KEY from `go run ./cmd/tproxy --print-master-key`
npm install
npm run dev
```

- Dashboard (hot reload): `http://127.0.0.1:28121/dashboard/`
- API gateway: `http://127.0.0.1:28120/v1`
- Embedded dashboard (production build): `http://127.0.0.1:28120/dashboard/`
- Health: `http://127.0.0.1:28120/healthz`

Manual backend only:

```bash
source .env.run
go run ./cmd/tproxy --config config.yaml
```

## Current implementation status

- SQLite schema and declarative YAML bootstrap.
- Public models, scoped aliases and ordered route targets.
- Hashed client API keys and encrypted provider credentials.
- OpenAI Chat Completions and Responses.
- OpenAI Responses over SSE and authenticated WebSocket.
- Claude Messages and count-tokens compatibility.
- Gemini generateContent compatibility.
- Generic OpenAI, Anthropic, Gemini and Ollama adapters.
- Vertex project-scoped Gemini adapter, specialized image/video provider aliases and an opt-in out-of-process HTTP plugin adapter.
- Native Antigravity Cloud Code adapter with Google OAuth enrollment and project bootstrap.
- Tavily web-search adapter behind virtual models and normal fallback policies.
- ElevenLabs TTS/STT adapter with OpenAI-compatible speech and transcription payloads.
- First-party Codex Responses and Claude OAuth provider profiles with provider-specific token exchange and headers.
- Codex proprietary device login plus Kimi and xAI RFC 8628 device login.
- Streaming normalization and public model-name rewriting.
- Retry, account cooldown and cross-provider fallback.
- Generic OAuth PKCE and device authorization, encrypted token envelopes, refresh deduplication and background refresh.
- Embedded React dashboard and management snapshot API.
- SQLite backup, restore and integrity-check CLI operations.
- Encrypted OAuth auth-bundle import/export using the same master key.
- Provider health/model discovery, ordered public-model combos, scoped aliases and JSON/YAML secret-free configuration import/export.
- Client-key endpoint/rate/concurrency/input/media/daily-budget policies, bounded SQLite request logs/audit events and retention cleanup.

See `../specs/07-delivery-plan.md` for staged capabilities.

The current implementation matrix is in `docs/IMPLEMENTATION.md`.

## Storage scope

SQLite is the only supported persistence backend. `tproxy` is designed for local, desktop and single-node server deployments; PostgreSQL and clustered database operation are out of scope.
