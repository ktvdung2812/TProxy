# tproxy — Agent Skills

Drop-in skills for any AI agent (Claude, Cursor, ChatGPT, custom SDK). Copy a link below and paste it to your AI — it will fetch the skill and use **tproxy** as the gateway.

> Tip: start with the **tproxy** entry skill — setup + index of all capabilities.

## Skills

| Capability | Path (local) |
|---|---|
| **Entry / Setup** | [`tproxy/SKILL.md`](./tproxy/SKILL.md) |
| Chat / code-gen | [`tproxy-chat/SKILL.md`](./tproxy-chat/SKILL.md) |
| Image generation | [`tproxy-image/SKILL.md`](./tproxy-image/SKILL.md) |
| Video generation | [`tproxy-video/SKILL.md`](./tproxy-video/SKILL.md) |
| Text-to-speech | [`tproxy-tts/SKILL.md`](./tproxy-tts/SKILL.md) |
| Speech-to-text | [`tproxy-stt/SKILL.md`](./tproxy-stt/SKILL.md) |
| Embeddings | [`tproxy-embeddings/SKILL.md`](./tproxy-embeddings/SKILL.md) |
| Web search | [`tproxy-web-search/SKILL.md`](./tproxy-web-search/SKILL.md) |
| Web fetch | [`tproxy-web-fetch/SKILL.md`](./tproxy-web-fetch/SKILL.md) |

## Configure once

```bash
export TPROXY_URL="http://127.0.0.1:28120"   # or tunnel URL
export TPROXY_KEY="sk-..."                   # Dashboard → APIs → client key
```

Verify: `curl -sS $TPROXY_URL/healthz`

## Notes

- Skills are **agent instructions**, not runtime plugins.
- Point agents at raw GitHub URLs after you push, or absolute `file://` / workspace paths locally.
- MITM capture (9Router) is **not** part of tproxy — use OAuth / API keys / cookies instead.
