import type { ClaudeMappingResponse } from "./api";

export type MappingTier = "default" | "fable" | "opus" | "sonnet" | "haiku";

export type ReasoningEffort = "" | "none" | "low" | "medium" | "high" | "xhigh" | "max";

const REASONING_EFFORT_BASE_OPTIONS: Array<{ value: ReasoningEffort; label: string }> = [
  { value: "", label: "Default" },
  { value: "none", label: "None" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "xhigh", label: "Extra high" },
];

const REASONING_EFFORT_MAX_OPTION = { value: "max" as const, label: "Max" };

export const REASONING_EFFORT_OPTIONS: Array<{ value: ReasoningEffort; label: string }> = [
  ...REASONING_EFFORT_BASE_OPTIONS,
  REASONING_EFFORT_MAX_OPTION,
];

export function reasoningEffortOptionsForTarget(
  target: string,
  current: ReasoningEffort = "",
): Array<{ value: ReasoningEffort; label: string }> {
  const model = target.trim().toLowerCase();
  const options: Array<{ value: ReasoningEffort; label: string }> = [...REASONING_EFFORT_BASE_OPTIONS];
  const isGpt56 = model.includes("gpt-5.6") || model.includes("codex-gpt-5.6");
  if (!model || isGpt56) {
    options.push(REASONING_EFFORT_MAX_OPTION);
  }
  if (current && !options.some((option) => option.value === current)) {
    const fallback = REASONING_EFFORT_OPTIONS.find((option) => option.value === current);
    if (fallback) {
      options.push(fallback);
    }
  }
  return options;
}

export const MAPPING_TIERS: MappingTier[] = ["default", "fable", "opus", "sonnet", "haiku"];

export const TIER_ENV_VARS: Record<MappingTier, string> = {
  default: "ANTHROPIC_DEFAULT_MODEL",
  fable: "ANTHROPIC_DEFAULT_FABLE_MODEL",
  opus: "ANTHROPIC_DEFAULT_OPUS_MODEL",
  sonnet: "ANTHROPIC_DEFAULT_SONNET_MODEL",
  haiku: "ANTHROPIC_DEFAULT_HAIKU_MODEL",
};

/** Placeholder tier names kept on the Claude Code client — tproxy rewrites server-side. */
export const CLAUDE_CLIENT_TIER_PLACEHOLDERS: Record<MappingTier, string> = {
  default: "default",
  fable: "fable",
  opus: "opus",
  sonnet: "sonnet",
  haiku: "haiku",
};

/** GPT codename placeholders for Codex CLI / OpenAI-compatible clients. */
export const GPT_CLIENT_TIER_PLACEHOLDERS: Partial<Record<MappingTier, string>> = {
  fable: "gpt-sol",
  opus: "gpt-terra",
  haiku: "gpt-luna",
};

export type GptCodenameTier = keyof typeof GPT_CLIENT_TIER_PLACEHOLDERS;

export const CODEX_MAPPING_TIERS = Object.keys(GPT_CLIENT_TIER_PLACEHOLDERS) as GptCodenameTier[];

/** Canonical placeholders shown in Mapping → Placeholder rewrite (Claude tab). */
export const CLAUDE_MAPPING_PLACEHOLDER_NAMES = ["default", "fable", "opus", "sonnet", "haiku"] as const;

/** Canonical placeholders shown in Mapping → Placeholder rewrite (Codex tab). */
export const CODEX_MAPPING_PLACEHOLDER_NAMES = ["gpt-sol", "gpt-terra", "gpt-luna"] as const;

export const CATALOG_PLACEHOLDER_NAMES = [
  ...CLAUDE_MAPPING_PLACEHOLDER_NAMES,
  ...CODEX_MAPPING_PLACEHOLDER_NAMES,
] as const;

export const DEFAULT_CLAUDE_PRIMARY_MODEL: MappingTier = "fable";
export const CLAUDE_CODE_CONTEXT_TOKENS = "1048576";

export type ClaudeClientEnvOptions = {
  baseUrl: string;
  apiKey: string;
  primaryModel?: MappingTier;
  subagentModel?: MappingTier;
};

export function buildClaudeCodeClientEnv(options: ClaudeClientEnvOptions): Record<string, string> {
  const primary = options.primaryModel ?? DEFAULT_CLAUDE_PRIMARY_MODEL;
  const subagent = options.subagentModel ?? primary;
  const key = options.apiKey.trim();
  return {
    ANTHROPIC_BASE_URL: options.baseUrl,
    ANTHROPIC_API_KEY: key,
    ANTHROPIC_MODEL: primary,
    ANTHROPIC_DEFAULT_MODEL: CLAUDE_CLIENT_TIER_PLACEHOLDERS.default,
    ANTHROPIC_DEFAULT_FABLE_MODEL: CLAUDE_CLIENT_TIER_PLACEHOLDERS.fable,
    ANTHROPIC_DEFAULT_OPUS_MODEL: CLAUDE_CLIENT_TIER_PLACEHOLDERS.opus,
    ANTHROPIC_DEFAULT_SONNET_MODEL: CLAUDE_CLIENT_TIER_PLACEHOLDERS.sonnet,
    ANTHROPIC_DEFAULT_HAIKU_MODEL: CLAUDE_CLIENT_TIER_PLACEHOLDERS.haiku,
    CLAUDE_CODE_SUBAGENT_MODEL: subagent,
    CLAUDE_CODE_AUTO_COMPACT_WINDOW: CLAUDE_CODE_CONTEXT_TOKENS,
    CLAUDE_CODE_MAX_CONTEXT_TOKENS: CLAUDE_CODE_CONTEXT_TOKENS,
  };
}

export function resolveTierTargets(
  overrides: Record<string, string>,
  data: ClaudeMappingResponse | null,
): Record<MappingTier, string> {
  const result = {} as Record<MappingTier, string>;
  for (const tier of MAPPING_TIERS) {
    const override = overrides[tier]?.trim();
    if (override) {
      result[tier] = override;
      continue;
    }
    result[tier] =
      data?.effective_resolved?.[tier]?.resolved ||
      data?.effective?.[tier] ||
      data?.defaults?.[tier] ||
      "";
  }
  return result;
}

export function buildServerEnv(targets: Record<MappingTier, string>): string {
  const lines = MAPPING_TIERS.filter((tier) => targets[tier]).map(
    (tier) => `${TIER_ENV_VARS[tier]}=${targets[tier]}`,
  );
  return lines.length > 0 ? lines.join("\n") : "# Configure tier targets on the Mapping tab first.";
}

export function buildClaudeSettings(
  baseUrl: string,
  apiKey: string,
  primaryModel: MappingTier = DEFAULT_CLAUDE_PRIMARY_MODEL,
): string {
  return JSON.stringify(
    {
      hasCompletedOnboarding: true,
      env: buildClaudeCodeClientEnv({ baseUrl, apiKey, primaryModel }),
    },
    null,
    2,
  );
}

export function buildCodexConfig(
  baseUrl: string,
  apiKey: string,
  primaryTier: GptCodenameTier = "fable",
): string {
  const model = GPT_CLIENT_TIER_PLACEHOLDERS[primaryTier] || "gpt-sol";
  const lines = [
    `model = "${model}"`,
    `model_provider = "tproxy"`,
    ``,
    `[model_providers.tproxy]`,
    `name = "tproxy"`,
    `base_url = "${baseUrl.replace(/\/v1\/?$/, "")}/v1"`,
    `wire_api = "responses"`,
    `env_key = "TPROXY_API_KEY"`,
    ``,
    `# Set TPROXY_API_KEY=${apiKey || "your-api-key"} in the shell before running codex`,
  ];
  return lines.join("\n");
}

export function buildBashExports(baseUrl: string, apiKey: string, primaryModel: MappingTier = DEFAULT_CLAUDE_PRIMARY_MODEL): string {
  return Object.entries(buildClaudeCodeClientEnv({ baseUrl, apiKey, primaryModel }))
    .map(([key, value]) => `export ${key}="${value}"`)
    .join("\n");
}

/** Bash exports for CLI Tools guide — uses tier placeholders or a virtual model ID from the dashboard. */
export function buildClaudeGuideExports(baseUrl: string, apiKey: string, model: string): string {
  const modelId = model.trim() || DEFAULT_CLAUDE_PRIMARY_MODEL;
  const tier = modelId.toLowerCase() as MappingTier;
  if (MAPPING_TIERS.includes(tier)) {
    return `${buildBashExports(baseUrl, apiKey, tier)}\nclaude`;
  }
  const env = buildClaudeCodeClientEnv({ baseUrl, apiKey, primaryModel: DEFAULT_CLAUDE_PRIMARY_MODEL });
  env.ANTHROPIC_MODEL = modelId;
  env.CLAUDE_CODE_SUBAGENT_MODEL = modelId;
  return `${Object.entries(env)
    .map(([entryKey, value]) => `export ${entryKey}="${value}"`)
    .join("\n")}\nclaude`;
}

export function buildPowerShellExports(
  baseUrl: string,
  apiKey: string,
  primaryModel: MappingTier = DEFAULT_CLAUDE_PRIMARY_MODEL,
): string {
  return Object.entries(buildClaudeCodeClientEnv({ baseUrl, apiKey, primaryModel }))
    .map(([key, value]) => `$env:${key} = "${value}"`)
    .join("\n");
}

export function buildDockerEnv(targets: Record<MappingTier, string>): string {
  const lines = MAPPING_TIERS.filter((tier) => targets[tier]).map(
    (tier) => `      - ${TIER_ENV_VARS[tier]}=${targets[tier]}`,
  );
  if (lines.length === 0) {
    return "# No tier overrides configured — set targets on the Mapping tab first.";
  }
  return `    environment:\n${lines.join("\n")}`;
}
