# TProxy Models

Contributes the models routed through a running [tproxy](../../README.md) gateway to
VS Code Chat via the Language Model Chat Provider API (`language-models` tag), so
they appear in Copilot Chat's **Manage Models** picker like any other provider.

## How it works

- Fetches `GET {baseUrl}/v1/models` and registers every catalog model with VS Code
  (tool-calling and vision capabilities are mapped from the tproxy catalog).
- Chat requests are converted to OpenAI format and streamed through
  `POST {baseUrl}/v1/chat/completions`, including tool calls and image inputs.
- Sends `Authorization: Bearer <key>` when an API key is configured. If tproxy runs
  with `allow-local-without-key` on loopback, no key is needed.

## Settings

| Setting | Default | Description |
|---|---|---|
| `tproxy.baseUrl` | `http://127.0.0.1:28120` | tproxy gateway base URL |

The API key is stored in VS Code Secret Storage, not settings. Run
**TProxy: Manage TProxy** (command `tproxy.manage`, also available from the
provider's gear icon in Manage Models) to set the base URL and key.

## Run / debug

No build step — plain JavaScript.

```sh
node selfcheck.js   # sanity-check the SSE/tool-call parsers
```

To try it: open this folder in VS Code, press `F5` (Extension Development Host),
then in the Chat model picker choose **Manage Models → TProxy**.
