export type CLIToolNote = {
  type: "info" | "warning" | "error" | "cloudCheck";
  text: string;
};

export type CLIToolGuideStep = {
  step: number;
  title: string;
  desc?: string;
  value?: string;
  copyable?: boolean;
  type?: "apiKeySelector" | "modelSelector";
  /** Which column renders this step in the setup guide. */
  column?: "config" | "commands";
};

export type CLIToolModelSlot = {
  id: string;
  name: string;
  alias: string;
};

export type CLITool = {
  id: string;
  name: string;
  icon: string;
  color: string;
  description: string;
  configType: "guide" | "env" | "custom" | "mitm";
  notes?: CLIToolNote[];
  guideSteps?: CLIToolGuideStep[];
  codeBlock?: { language: string; code: string };
  docsUrl?: string;
  requiresExternalUrl?: boolean;
  defaultCommand?: string;
  settingsFile?: string;
  defaultModels?: CLIToolModelSlot[];
  mitmDomain?: string;
};

/** Tools with backend auto-apply (localhost) — mirrors 9router CLI-tools API */
export const AUTO_APPLY_TOOL_IDS = [
  "claude",
  "codex",
  "openclaw",
  "opencode",
  "cline",
  "droid",
  "kilo",
  "deepseek-tui",
  "hermes",
  "grok-build",
  "jcode",
] as const;

export type AutoApplyToolId = (typeof AUTO_APPLY_TOOL_IDS)[number];

export function isAutoApplyTool(toolId: string): toolId is AutoApplyToolId {
  return (AUTO_APPLY_TOOL_IDS as readonly string[]).includes(toolId);
}

const CUSTOM_GUIDE_STEPS: CLIToolGuideStep[] = [
  { step: 1, title: "API Key", type: "apiKeySelector", column: "config" },
  { step: 2, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
  { step: 3, title: "Model", type: "modelSelector", column: "config" },
];

/** CLI tools — parity with 9router /dashboard/cli-tools */
export const CLI_TOOLS: Record<string, CLITool> = {
  claude: {
    id: "claude",
    name: "Claude Code",
    icon: "terminal",
    color: "#D97757",
    description: "Anthropic Claude Code CLI",
    configType: "env",
    settingsFile: "~/.claude/settings.json",
    notes: [
      { type: "info", text: "Set ANTHROPIC_BASE_URL to your tproxy endpoint and use a client API key from APIs." },
      { type: "info", text: "Use a combo ID (for example claude-code-fallback) as your default model to get ordered fallback across virtual models." },
    ],
    guideSteps: [
      { step: 1, title: "Install Claude Code", desc: "npm install -g @anthropic-ai/claude-code", column: "commands" },
      { step: 2, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 3, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
      { step: 4, title: "Default model", type: "modelSelector", column: "config" },
    ],
    defaultModels: [
      { id: "fable", name: "Claude Fable", alias: "fable" },
      { id: "opus", name: "Claude Opus", alias: "opus" },
      { id: "sonnet", name: "Claude Sonnet", alias: "sonnet" },
      { id: "haiku", name: "Claude Haiku", alias: "haiku" },
    ],
    codeBlock: {
      language: "bash",
      code: `export ANTHROPIC_BASE_URL="{{baseUrl}}"
export ANTHROPIC_API_KEY="{{apiKey}}"
export ANTHROPIC_MODEL="fable"
export ANTHROPIC_DEFAULT_FABLE_MODEL="fable"
export ANTHROPIC_DEFAULT_OPUS_MODEL="opus"
export ANTHROPIC_DEFAULT_SONNET_MODEL="sonnet"
export ANTHROPIC_DEFAULT_HAIKU_MODEL="haiku"
export CLAUDE_CODE_SUBAGENT_MODEL="fable"
claude`,
    },
  },
  openclaw: {
    id: "openclaw",
    name: "Open Claw",
    icon: "pets",
    color: "#FF6B35",
    description: "Open Claw AI Assistant",
    configType: "custom",
    settingsFile: "~/.openclaw/openclaw.json",
    notes: [
      { type: "info", text: "Open Claw stores provider config under models.providers. Add a tproxy provider entry with your base URL and API key." },
    ],
    guideSteps: [
      { step: 1, title: "Install Open Claw", desc: "Follow the Open Claw installation guide for your platform.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
      { step: 5, title: "Provider name", desc: 'Use provider id "tproxy" in your Open Claw config.', column: "commands" },
    ],
    codeBlock: {
      language: "json",
      code: `{
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
    },
  },
  codex: {
    id: "codex",
    name: "OpenAI Codex CLI / App",
    icon: "code",
    color: "#10A37F",
    description: "OpenAI Codex CLI",
    configType: "custom",
    settingsFile: "~/.codex/config.toml",
    notes: [
      { type: "info", text: "Codex reads ~/.codex/config.toml. Point base_url at tproxy's OpenAI-compatible /v1 endpoint." },
    ],
    guideSteps: [
      { step: 1, title: "Install Codex", desc: "Follow OpenAI's Codex CLI installation guide.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
    codeBlock: {
      language: "toml",
      code: `model = "{{model}}"
base_url = "{{baseUrl}}"
api_key = "{{apiKey}}"`,
    },
  },
  opencode: {
    id: "opencode",
    name: "OpenCode",
    icon: "terminal",
    color: "#E87040",
    description: "OpenCode AI Terminal Assistant",
    configType: "custom",
    notes: [
      { type: "info", text: "Configure OpenCode to use tproxy as an OpenAI-compatible provider." },
    ],
    guideSteps: CUSTOM_GUIDE_STEPS,
    codeBlock: {
      language: "json",
      code: `{
  "provider": "openai",
  "baseURL": "{{baseUrl}}",
  "apiKey": "{{apiKey}}",
  "model": "{{model}}"
}`,
    },
  },
  cowork: {
    id: "cowork",
    name: "Claude Cowork",
    icon: "groups",
    color: "#D97757",
    description: "Claude Desktop Cowork (third-party inference)",
    configType: "custom",
    notes: [
      { type: "info", text: "Cowork routes Claude Desktop through a custom inference endpoint. Point it at tproxy with your API key and allowed models." },
      { type: "info", text: "Add a combo ID such as cowork-fallback to allowed models for ordered fallback across virtual models." },
    ],
    guideSteps: [
      { step: 1, title: "Open Cowork settings", desc: "Configure third-party inference in Claude Desktop Cowork.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
      { step: 5, title: "Allowed models", desc: "Add one or more tproxy virtual model IDs to the allowed models list.", column: "commands" },
    ],
  },
  hermes: {
    id: "hermes",
    name: "Hermes Agent",
    icon: "psychology",
    color: "#8B5CF6",
    description: "Nous Research self-improving AI agent",
    configType: "custom",
    notes: [
      { type: "info", text: "Hermes Agent uses a model config with base_url, api_key, and model fields." },
    ],
    guideSteps: CUSTOM_GUIDE_STEPS,
    codeBlock: {
      language: "json",
      code: `{
  "model": {
    "base_url": "{{baseUrl}}",
    "api_key": "{{apiKey}}",
    "model": "{{model}}"
  }
}`,
    },
  },
  droid: {
    id: "droid",
    name: "Factory Droid",
    icon: "smart_toy",
    color: "#00D4FF",
    description: "Factory Droid AI Assistant",
    configType: "custom",
    notes: [
      { type: "info", text: "Droid supports custom model providers. Add a tproxy entry under customModels." },
    ],
    guideSteps: CUSTOM_GUIDE_STEPS,
    codeBlock: {
      language: "json",
      code: `{
  "customModels": [{
    "id": "custom:tproxy-0",
    "model": "{{model}}",
    "baseUrl": "{{baseUrl}}",
    "apiKey": "{{apiKey}}"
  }]
}`,
    },
  },
  cursor: {
    id: "cursor",
    name: "Cursor",
    icon: "edit",
    color: "#000000",
    description: "Cursor AI Code Editor",
    configType: "guide",
    requiresExternalUrl: true,
    notes: [
      { type: "warning", text: "Requires Cursor Pro account to use this feature." },
      {
        type: "cloudCheck",
        text: "Cursor routes requests through its own server, so a local-only endpoint is not supported. Use a publicly reachable tproxy URL.",
      },
    ],
    guideSteps: [
      { step: 1, title: "Open Settings", desc: "Go to Settings → Models", column: "commands" },
      { step: 2, title: "Enable OpenAI API", desc: 'Enable the "OpenAI API key" option', column: "commands" },
      { step: 3, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
      { step: 4, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 5, title: "Add Custom Model", desc: 'Click "View All Models" → "Add Custom Model"', column: "commands" },
      { step: 6, title: "Select Model", type: "modelSelector", column: "config" },
      { step: 7, title: "Use model ID", desc: "Enter the virtual model ID from tproxy as the custom model name in Cursor.", column: "commands" },
    ],
  },
  cline: {
    id: "cline",
    name: "Cline",
    icon: "extension",
    color: "#00D1B2",
    description: "Cline AI Coding Assistant",
    configType: "custom",
    guideSteps: [
      { step: 1, title: "Open Cline settings", desc: "Choose API Provider → OpenAI Compatible", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
  },
  kilo: {
    id: "kilo",
    name: "Kilo Code",
    icon: "code_blocks",
    color: "#FF6B6B",
    description: "Kilo Code AI Assistant",
    configType: "custom",
    notes: [
      { type: "info", text: "Kilo Code VS Code extension supports OpenAI-compatible endpoints." },
    ],
    guideSteps: [
      { step: 1, title: "Open Kilo settings", desc: "Select API Provider → OpenAI Compatible", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
  },
  roo: {
    id: "roo",
    name: "Roo",
    icon: "smart_toy",
    color: "#FF6B6B",
    description: "Roo AI Assistant",
    configType: "guide",
    guideSteps: [
      { step: 1, title: "Open Settings", desc: "Go to Roo Settings panel", column: "commands" },
      { step: 2, title: "Select Provider", desc: "Choose API Provider → Ollama", column: "commands" },
      { step: 3, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
      { step: 4, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 5, title: "Select Model", type: "modelSelector", column: "config" },
    ],
  },
  continue: {
    id: "continue",
    name: "Continue",
    icon: "play_arrow",
    color: "#7C3AED",
    description: "Continue AI Assistant",
    configType: "guide",
    guideSteps: [
      { step: 1, title: "Open Config", desc: "Open Continue configuration file", column: "commands" },
      { step: 2, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 3, title: "Select Model", type: "modelSelector", column: "config" },
      { step: 4, title: "Add Model Config", desc: "Add the following configuration to your models array:", column: "commands" },
    ],
    codeBlock: {
      language: "json",
      code: `{
  "apiBase": "{{baseUrl}}",
  "title": "{{model}}",
  "model": "{{model}}",
  "provider": "openai",
  "apiKey": "{{apiKey}}"
}`,
    },
  },
  amp: {
    id: "amp",
    name: "Amp CLI",
    icon: "bolt",
    color: "#F97316",
    description: "Sourcegraph Amp coding assistant CLI",
    configType: "guide",
    defaultCommand: "amp",
    notes: [
      { type: "info", text: "Use tproxy virtual model IDs to keep Amp shorthand mappings stable across provider updates." },
      {
        type: "warning",
        text: "Suggested shorthand examples: g25p → gemini-2.5-pro, g25f → gemini-2.5-flash, cs45 → claude-sonnet-4-5.",
      },
    ],
    guideSteps: [
      { step: 1, title: "Install Amp", desc: "Install the Amp CLI using the package manager supported by your environment.", column: "commands" },
      { step: 2, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 3, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
      { step: 4, title: "Select Model", type: "modelSelector", column: "config" },
      { step: 5, title: "Add Shorthands", desc: "Map Amp shorthand names such as g25p or cs45 to tproxy model IDs in your local config.", column: "commands" },
    ],
    codeBlock: {
      language: "bash",
      code: `export OPENAI_API_KEY="{{apiKey}}"
export OPENAI_BASE_URL="{{baseUrl}}"
amp --model "{{model}}"
# Example shorthand aliases you can map locally:
# g25p -> gemini-2.5-pro
# cs45 -> claude-sonnet-4-5`,
    },
  },
  qwen: {
    id: "qwen",
    name: "Qwen Code",
    icon: "terminal",
    color: "#10B981",
    description: "Alibaba Qwen Code CLI — supports OpenAI, Anthropic & Gemini providers via tproxy",
    docsUrl: "https://qwenlm.github.io/qwen-code-docs/en/users/configuration/model-providers/",
    configType: "guide",
    defaultCommand: "qwen",
    notes: [
      {
        type: "info",
        text: "Qwen Code supports multiple provider types (openai, anthropic, gemini) via modelProviders in settings.json. tproxy works as an OpenAI-compatible endpoint.",
      },
      {
        type: "info",
        text: "Any model available in tproxy can be used — not just Qwen models.",
      },
      { type: "warning", text: "Config path: Linux/macOS ~/.qwen/settings.json • Windows %USERPROFILE%\\.qwen\\settings.json" },
      {
        type: "error",
        text: "Qwen OAuth free tier was discontinued. Use tproxy with your configured upstream providers instead.",
      },
    ],
    defaultModels: [
      { id: "coder-model", name: "Coder Model", alias: "coder-model" },
      { id: "qwen3-coder-plus", name: "Qwen 3 Coder Plus", alias: "qwen3-coder-plus" },
      { id: "qwen3-coder-flash", name: "Qwen 3 Coder Flash", alias: "qwen3-coder-flash" },
      { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", alias: "claude-sonnet-4-6" },
      { id: "gemini-3-flash", name: "Gemini 3 Flash", alias: "gemini-3-flash" },
    ],
    guideSteps: [
      { step: 1, title: "Install Qwen Code", desc: "npm install -g @qwen-code/qwen-code", column: "commands" },
      { step: 2, title: "API Key", type: "apiKeySelector", column: "config" },
      { step: 3, title: "Base URL", value: "{{baseUrl}}", copyable: true, column: "config" },
      { step: 4, title: "Select Model", type: "modelSelector", column: "config" },
      { step: 5, title: "Save Config", desc: "Copy the JSON below to your ~/.qwen/settings.json file.", column: "commands" },
    ],
    codeBlock: {
      language: "json",
      code: `{
  "security": {
    "auth": {
      "selectedType": "openai",
      "apiKey": "{{apiKey}}",
      "baseUrl": "{{baseUrl}}"
    }
  },
  "model": {
    "name": "{{model}}"
  }
}`,
    },
  },
  "deepseek-tui": {
    id: "deepseek-tui",
    name: "DeepSeek TUI",
    icon: "terminal",
    color: "#4D6BFE",
    description: "DeepSeek Terminal Coding Agent (Rust TUI)",
    docsUrl: "https://github.com/DeepSeek-TUI/DeepSeek-TUI",
    configType: "custom",
    defaultCommand: "deepseek",
    settingsFile: "~/.deepseek/config.toml",
    notes: [
      {
        type: "info",
        text: "DeepSeek TUI uses ~/.deepseek/config.toml. Set the provider to openai mode with your base_url, api_key, and model.",
      },
      { type: "warning", text: "Config path: Linux/macOS ~/.deepseek/config.toml • Windows %USERPROFILE%\\.deepseek\\config.toml" },
    ],
    defaultModels: [
      { id: "deepseek-v4-pro", name: "DeepSeek V4 Pro", alias: "deepseek-v4-pro" },
      { id: "deepseek-v4-flash", name: "DeepSeek V4 Flash", alias: "deepseek-v4-flash" },
      { id: "deepseek-chat", name: "DeepSeek V3 Chat", alias: "deepseek-chat" },
    ],
    guideSteps: [
      { step: 1, title: "Install DeepSeek TUI", desc: "Follow the DeepSeek TUI installation guide.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
    codeBlock: {
      language: "toml",
      code: `[providers.openai]
base_url = "{{baseUrl}}"
api_key = "{{apiKey}}"
model = "{{model}}"`,
    },
  },
  jcode: {
    id: "jcode",
    name: "jcode",
    icon: "speed",
    color: "#FF6B35",
    description: "High-performance Rust-based coding agent harness",
    docsUrl: "https://github.com/1jehuang/jcode",
    configType: "custom",
    notes: [
      {
        type: "info",
        text: "jcode is a Rust-based coding agent with semantic memory, multi-agent swarms, and extreme performance.",
      },
      {
        type: "info",
        text: "Configure tproxy as an OpenAI-compatible provider to route all jcode requests through tproxy.",
      },
      {
        type: "warning",
        text: "Install via: curl -fsSL https://raw.githubusercontent.com/1jehuang/jcode/master/scripts/install.sh | bash",
      },
    ],
    defaultModels: [
      { id: "claude-opus-4-7", name: "Claude Opus 4.7", alias: "opus" },
      { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", alias: "sonnet" },
      { id: "gpt-5.5", name: "GPT 5.5", alias: "gpt5" },
      { id: "gemini-3.1-pro", name: "Gemini 3.1 Pro", alias: "gemini" },
    ],
    guideSteps: [
      { step: 1, title: "Install jcode", desc: "Run the install script from the jcode repository.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
    codeBlock: {
      language: "json",
      code: `{
  "providers": {
    "tproxy": {
      "base_url": "{{baseUrl}}",
      "api_key": "{{apiKey}}",
      "model": "{{model}}"
    }
  }
}`,
    },
  },
  "grok-build": {
    id: "grok-build",
    name: "Grok Build",
    icon: "construction",
    color: "#1DA1F2",
    description: "xAI Grok Build TUI coding agent",
    docsUrl: "https://x.ai/cli",
    configType: "custom",
    defaultCommand: "grok",
    settingsFile: "~/.grok/config.toml",
    notes: [
      {
        type: "info",
        text: "Grok Build uses ~/.grok/config.toml. TProxy writes a [model.tproxy] custom model and sets it as the default.",
      },
      {
        type: "info",
        text: 'After Apply, run grok (or /model tproxy) to use the routed model. Switch back anytime with /model grok-build.',
      },
      { type: "warning", text: "Config path: Linux/macOS ~/.grok/config.toml • Windows %USERPROFILE%\\.grok\\config.toml" },
    ],
    guideSteps: [
      { step: 1, title: "Install Grok Build", desc: "Follow xAI's Grok CLI installation guide.", column: "commands" },
      ...CUSTOM_GUIDE_STEPS.map((step, index) => ({ ...step, step: index + 2 })),
    ],
    codeBlock: {
      language: "toml",
      code: `[models]
default = "tproxy"

[model.tproxy]
model = "{{model}}"
base_url = "{{baseUrl}}"
name = "TProxy"
description = "Routed via TProxy gateway"
api_backend = "chat_completions"
api_key = "{{apiKey}}"`,
    },
  },
};

/** MITM IDE tools — same set as 9router MITM_TOOLS */
export const MITM_TOOLS: Record<string, CLITool> = {
  antigravity: {
    id: "antigravity",
    name: "Antigravity",
    icon: "flight",
    color: "#4285F4",
    description: "Google Antigravity IDE with MITM",
    configType: "mitm",
    mitmDomain: "daily-cloudcode-pa.googleapis.com",
    defaultModels: [
      { id: "gemini-3.5-flash-low", name: "Gemini 3.5 Flash (Medium) / Default", alias: "gemini-3.5-flash-low" },
      { id: "gemini-3-flash-agent", name: "Gemini 3.5 Flash (High)", alias: "gemini-3-flash-agent" },
      { id: "gemini-3.1-pro-low", name: "Gemini 3.1 Pro (Low)", alias: "gemini-3.1-pro-low" },
      { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6 (Thinking)", alias: "claude-sonnet-4-6" },
      { id: "claude-opus-4-6-thinking", name: "Claude Opus 4.6 (Thinking)", alias: "claude-opus-4-6-thinking" },
      { id: "gemini-3-flash", name: "Gemini 3 Flash (Command)", alias: "gemini-3-flash" },
    ],
    notes: [
      { type: "warning", text: "MITM proxy is not yet available in tproxy. Model mappings below show the slots Antigravity expects once MITM ships." },
    ],
  },
  copilot: {
    id: "copilot",
    name: "GitHub Copilot",
    icon: "code",
    color: "#1F6FEB",
    description: "GitHub Copilot IDE with MITM",
    configType: "mitm",
    mitmDomain: "api.individual.githubcopilot.com",
    defaultModels: [
      { id: "gpt-5-mini", name: "GPT-5 mini", alias: "gpt-5-mini" },
      { id: "gpt-5.4-nano", name: "GPT-5.4 nano", alias: "gpt-5.4-nano" },
      { id: "claude-haiku-4.5", name: "Claude Haiku 4.5", alias: "claude-haiku-4.5" },
      { id: "gpt-4o", name: "GPT-4o", alias: "gpt-4o" },
      { id: "gpt-4.1", name: "GPT-4.1", alias: "gpt-4.1" },
    ],
    notes: [
      { type: "warning", text: "MITM proxy is not yet available in tproxy. Copilot CLI defaults to gpt-5-mini — map that slot to a tproxy model when MITM is enabled." },
    ],
  },
  kiro: {
    id: "kiro",
    name: "Kiro",
    icon: "memory",
    color: "#FF6B00",
    description: "Kiro IDE with MITM",
    configType: "mitm",
    mitmDomain: "q.us-east-1.amazonaws.com",
    defaultModels: [
      { id: "claude-sonnet-5", name: "Claude Sonnet 5", alias: "claude-sonnet-5" },
      { id: "claude-sonnet-4.5", name: "Claude Sonnet 4.5", alias: "claude-sonnet-4.5" },
      { id: "claude-haiku-4.5", name: "Claude Haiku 4.5", alias: "claude-haiku-4.5" },
      { id: "deepseek-3.2", name: "DeepSeek 3.2", alias: "deepseek-3.2" },
      { id: "simple-task", name: "Qwen3 Coder Next", alias: "simple-task" },
    ],
    notes: [
      { type: "warning", text: "MITM proxy is not yet available in tproxy. Kiro dispatches concrete model IDs — each slot below should map to a tproxy virtual model." },
    ],
  },
};

export const ALL_CLI_TOOL_IDS = Object.keys(CLI_TOOLS);
export const ALL_MITM_TOOL_IDS = Object.keys(MITM_TOOLS);

export function getCLITool(toolId: string): CLITool | undefined {
  return CLI_TOOLS[toolId];
}

export function getMitmTool(toolId: string): CLITool | undefined {
  return MITM_TOOLS[toolId];
}

export function getAnyTool(toolId: string): CLITool | undefined {
  return CLI_TOOLS[toolId] ?? MITM_TOOLS[toolId];
}
