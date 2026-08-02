---
name: tproxy
description: Entry point for tproxy — self-hosted AI gateway with OpenAI/Claude/Gemini-compatible REST, combos, quota tracking, and multi-provider fallback. Use when the user mentions tproxy, TPROXY_URL, or wants one endpoint for many AI providers.
---

# tproxy

Self-hosted AI gateway. One base URL + client key; routes across OAuth subscriptions (Claude Code, Codex, Grok Build, …), API keys, and web-cookie providers.

## Setup

```bash
export TPROXY_URL="http://127.0.0.1:28120"   # or Cloudflare tunnel / Tailscale URL
export TPROXY_KEY="sk-..."                   # Dashboard → APIs (client API key)
```

Requests go to `${TPROXY_URL}/v1/...` with `Authorization: Bearer ${TPROXY_KEY}` (omit key only if local auth is disabled).

Verify:

```bash
curl -sS "$TPROXY_URL/healthz"
curl -sS "$TPROXY_URL/v1/models" -H "Authorization: Bearer $TPROXY_KEY"
```

## Discover models

```bash
curl -sS "$TPROXY_URL/v1/models" -H "Authorization: Bearer $TPROXY_KEY"
```

Use `data[].id` as the `model` field. Combos appear as virtual models with ordered fallback.

## Capability skills

| Capability | Skill |
|---|---|
| Chat / code-gen | `skills/tproxy-chat/SKILL.md` |
| Image | `skills/tproxy-image/SKILL.md` |
| Video | `skills/tproxy-video/SKILL.md` |
| TTS | `skills/tproxy-tts/SKILL.md` |
| STT | `skills/tproxy-stt/SKILL.md` |
| Embeddings | `skills/tproxy-embeddings/SKILL.md` |
| Web search | `skills/tproxy-web-search/SKILL.md` |
| Web fetch | `skills/tproxy-web-fetch/SKILL.md` |

Fetch the matching skill file when the user needs that modality.

## Errors

- **401** — set/refresh `TPROXY_KEY` (Dashboard → APIs)
- **404 model** — create a public model / combo in Dashboard → PPM
- **upstream / quota** — check Dashboard → Quota Tracker and Providers
