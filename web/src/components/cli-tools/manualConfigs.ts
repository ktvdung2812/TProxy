import type { CLITool } from "../../cli-tools/constants";
import type { ManualConfigEntry } from "./ManualConfigModal";

function replaceVars(text: string, baseUrl: string, apiKey: string, model: string): string {
  return text
    .replace(/\{\{baseUrl\}\}/g, baseUrl)
    .replace(/\{\{apiKey\}\}/g, apiKey || "your-api-key")
    .replace(/\{\{model\}\}/g, model || "virtual-model-id");
}

export function buildManualConfigs(
  tool: CLITool,
  baseUrl: string,
  apiKey: string,
  model: string,
): ManualConfigEntry[] {
  const key = apiKey.trim() || "your-api-key";
  const modelId = model || "virtual-model-id";

  if (tool.id === "claude") {
    const env: Record<string, string> = {
      ANTHROPIC_BASE_URL: baseUrl,
      ANTHROPIC_AUTH_TOKEN: key,
      ANTHROPIC_API_KEY: key,
    };
    if (modelId) env.ANTHROPIC_DEFAULT_SONNET_MODEL = modelId;
    return [
      {
        filename: tool.settingsFile ?? "~/.claude/settings.json",
        content: JSON.stringify({ hasCompletedOnboarding: true, env }, null, 2),
      },
    ];
  }

  if (tool.id === "codex") {
    return [
      {
        filename: "~/.codex/config.toml",
        content: replaceVars(
          `model = "{{model}}"
model_provider = "tproxy"

[model_providers.tproxy]
name = "TProxy"
base_url = "{{baseUrl}}"
wire_api = "responses"

[agents.subagent]
model = "{{model}}"`,
          baseUrl,
          key,
          modelId,
        ),
      },
      {
        filename: "~/.codex/auth.json",
        content: JSON.stringify({ OPENAI_API_KEY: key, auth_mode: "apikey" }, null, 2),
      },
    ];
  }

  if (tool.id === "openclaw") {
    return [
      {
        filename: tool.settingsFile ?? "~/.openclaw/openclaw.json",
        content: replaceVars(
          tool.codeBlock?.code ??
            `{
  "models": {
    "providers": {
      "tproxy": {
        "baseUrl": "{{baseUrl}}",
        "apiKey": "{{apiKey}}"
      }
    }
  },
  "agents": {
    "defaults": {
      "model": { "primary": "tproxy/{{model}}" }
    }
  }
}`,
          baseUrl,
          key,
          modelId,
        ),
      },
    ];
  }

  if (tool.settingsFile && tool.codeBlock?.code) {
    return [
      {
        filename: tool.settingsFile,
        content: replaceVars(tool.codeBlock.code, baseUrl, key, modelId),
      },
    ];
  }

  if (tool.codeBlock?.code) {
    return [
      {
        filename: `${tool.id}-config`,
        content: replaceVars(tool.codeBlock.code, baseUrl, key, modelId),
      },
    ];
  }

  return [
    {
      filename: "tproxy-endpoint",
      content: `base_url = ${baseUrl}\napi_key = ${key}\nmodel = ${modelId}`,
    },
  ];
}
