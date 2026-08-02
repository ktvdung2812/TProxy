---
name: tproxy-video
description: Video generation via tproxy /v1/videos endpoints. Use when the user wants text-to-video or image-to-video through the gateway.
---

# tproxy — Video

```bash
curl -X POST "$TPROXY_URL/v1/videos/generations" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_VIDEO_MODEL","prompt":"slow pan over mountains"}'
```

Poll status: `GET $TPROXY_URL/v1/videos/{job_id}` with the same auth header.
