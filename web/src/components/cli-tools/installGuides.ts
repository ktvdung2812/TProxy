/**
 * Inline install instructions shown when a tool is not detected locally — the
 * dashboard should not force a trip to the docs just to run one npm command.
 */
export type InstallGuide = {
  steps: string[];
  command?: string;
  docsUrl?: string;
};

export const INSTALL_GUIDES: Record<string, InstallGuide> = {
  claude: {
    steps: ["Install the Claude Code CLI, then run `claude` once to verify."],
    command: "npm install -g @anthropic-ai/claude-code",
    docsUrl: "https://docs.claude.com/en/docs/claude-code",
  },
  codex: {
    steps: ["Install the Codex CLI, then run `codex` once to verify."],
    command: "npm install -g @openai/codex",
    docsUrl: "https://developers.openai.com/codex/cli",
  },
  opencode: {
    steps: ["Install OpenCode, then run `opencode` once to create its config."],
    command: "npm install -g opencode-ai",
    docsUrl: "https://opencode.ai/docs",
  },
  droid: {
    steps: ["Install Factory Droid via the official installer script."],
    command: "curl -fsSL https://app.factory.ai/cli | sh",
    docsUrl: "https://docs.factory.ai",
  },
  openclaw: {
    steps: ["Install Open Claw, then run `openclaw` once to create ~/.openclaw/openclaw.json."],
    command: "npm install -g openclaw",
  },
  cline: {
    steps: ["Install the Cline extension in VS Code, then open it once so ~/.cline is created."],
    docsUrl: "https://docs.cline.bot",
  },
  kilo: {
    steps: ["Install the Kilo Code extension, then sign in once so its auth file is created."],
    docsUrl: "https://kilocode.ai/docs",
  },
  "deepseek-tui": {
    steps: ["Install DeepSeek TUI, then run `deepseek` once to create ~/.deepseek/config.toml."],
    docsUrl: "https://github.com/DeepSeek-TUI/DeepSeek-TUI",
  },
  hermes: {
    steps: ["Install the Hermes agent, then run `hermes` once to create ~/.hermes/config.yaml."],
    docsUrl: "https://github.com/NousResearch",
  },
  "grok-build": {
    steps: ["Install Grok Build, then run `grok` once to create ~/.grok/config.toml."],
    docsUrl: "https://x.ai/cli",
  },
  jcode: {
    steps: ["Install jcode via the official installer script."],
    command: "curl -fsSL https://raw.githubusercontent.com/1jehuang/jcode/master/scripts/install.sh | bash",
    docsUrl: "https://github.com/1jehuang/jcode",
  },
  copilot: {
    steps: [
      "Install VS Code and the GitHub Copilot Chat extension.",
      "tproxy writes a BYOK provider into chatLanguageModels.json; pick it from the model picker.",
    ],
    docsUrl: "https://code.visualstudio.com/docs/copilot/language-models",
  },
  cowork: {
    steps: [
      "Install Claude Desktop and open it once.",
      "Applying switches it into Cowork (3p) mode, then quit and reopen the app.",
    ],
    docsUrl: "https://claude.com/download",
  },
};
