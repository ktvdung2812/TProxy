---
name: tproxy-embeddings
description: Embeddings via tproxy OpenAI-compatible /v1/embeddings.
---

# tproxy — Embeddings

```bash
curl -X POST "$TPROXY_URL/v1/embeddings" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_EMBED_MODEL","input":"hello world"}'
```
