---
name: tproxy-web-search
description: Web search via tproxy /v1/search (Tavily and compatible search providers).
---

# tproxy — Web search

```bash
curl -X POST "$TPROXY_URL/v1/search" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_SEARCH_MODEL","query":"latest tproxy release"}'
```
