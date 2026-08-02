---
name: tproxy-tts
description: Text-to-speech via tproxy OpenAI-compatible /v1/audio/speech (ElevenLabs and compatible backends).
---

# tproxy — TTS

```bash
curl -X POST "$TPROXY_URL/v1/audio/speech" \
  -H "Authorization: Bearer $TPROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"YOUR_TTS_MODEL","input":"Hello from tproxy","voice":"alloy"}' \
  --output speech.mp3
```

Voices (when supported): `GET $TPROXY_URL/v1/audio/voices`.
