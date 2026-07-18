import type { ClaudeMappingResponse } from "./api";

export type MappingTier = "fable" | "opus" | "sonnet" | "haiku";

export const MAPPING_TIERS: MappingTier[] = ["fable", "opus", "sonnet", "haiku"];

export const TIER_ENV_VARS: Record<MappingTier, string> = {
  fable: "ANTHROPIC_DEFAULT_FABLE_MODEL",
  opus: "ANTHROPIC_DEFAULT_OPUS_MODEL",
  sonnet: "ANTHROPIC_DEFAULT_SONNET_MODEL",
  haiku: "ANTHROPIC_DEFAULT_HAIKU_MODEL",
};

/** Placeholder tier names kept on the Claude Code client — tproxy rewrites server-side. */
export const CLAUDE_CLIENT_TIER_PLACEHOLDERS: Record<MappingTier, string> = {
  fable: "fable",
  opus: "opus",
  sonnet: "sonnet",
  haiku: "haiku",
};

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

export function buildBashExports(baseUrl: string, apiKey: string, primaryModel: MappingTier = DEFAULT_CLAUDE_PRIMARY_MODEL): string {
  return Object.entries(buildClaudeCodeClientEnv({ baseUrl, apiKey, primaryModel }))
    .map(([key, value]) => `export ${key}="${value}"`)
    .join("\n");
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
