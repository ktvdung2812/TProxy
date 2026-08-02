---
name: tproxy-web-fetch
description: Fetch a URL to markdown/text via tproxy SSRF-protected web fetch helpers when exposed; otherwise guide the agent to use allowed tools through chat models with browsing.
---

# tproxy — Web fetch

Prefer chat models with web tools, or a search provider (`skills/tproxy-web-search/SKILL.md`).

If your deployment exposes a fetch endpoint through a public model or plugin, call it with the same `TPROXY_URL` / `TPROXY_KEY` auth pattern as other `/v1/*` routes.

Always respect SSRF limits — do not request internal/metadata IPs.
