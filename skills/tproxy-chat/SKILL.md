---
name: tproxy-chat
description: Chat / code generation via tproxy using OpenAI /v1/chat/completions, Anthropic /v1/messages, or OpenAI Responses. Use when the user wants to ask an LLM, generate code, or stream completions through tproxy.
---

# tproxy — Chat

Requires `TPROXY_URL` and usually `TPROXY_KEY`. See `skills/tproxy/SKILL.md` for setup.

## Endpoints

- `POST $TPROXY_URL/v1/chat/completions` — OpenAI Chat Completions
- `POST $TPROXY_URL/v1/messages` — Anthropic Messages
- `POST $TPROXY_URL/v1/responses` — OpenAI Responses

## OpenAI format

```bash
curl -X POST "$TPROXY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_PUBLIC_MODEL","messages":[{"role":"user","content":"Hi"}],"stream":false}'
```

Streaming: `"stream":true` → SSE `data: {...}` … `data: [DONE]`.

## Anthropic format

```bash
curl -X POST "$TPROXY_URL/v1/messages" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_PUBLIC_MODEL","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}'
```

## Combos

Point `model` at a combo ID (Dashboard → Combos) for ordered fallback across providers when quota or errors hit.

## Optional headers

- `X-TProxy-Compression: off` — disable token savers for one request
- `X-TProxy-Route-Model: <id>` — override route target
