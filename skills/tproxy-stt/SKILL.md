---
name: tproxy-stt
description: Speech-to-text via tproxy /v1/audio/transcriptions.
---

# tproxy — STT

```bash
curl -X POST "$TPROXY_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -F "file=@audio.mp3" \
  -F "model=YOUR_STT_MODEL"
```
