---
name: tproxy-image
description: Image generation via tproxy OpenAI-compatible /v1/images/generations. Use when the user wants to generate or edit images through the gateway.
---

# tproxy — Image

```bash
curl -X POST "$TPROXY_URL/v1/images/generations" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_IMAGE_MODEL","prompt":"a cat astronaut","n":1}'
```

Edits (when upstream supports): `POST $TPROXY_URL/v1/images/edits` (multipart).

List image-capable public models from Dashboard → PPM or `/v1/models` (filter by image capability when exposed).
