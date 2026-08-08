import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import type { ModelOption } from "../../lib/modelOptions";
import { MAPPING_TIERS, TIER_ENV_VARS, type MappingTier } from "../mapping/codegen";
import { fetchAdminSettings, saveGatewaySettings } from "../settings/api";
import { Field, Select, Toggle } from "../ui";

/** Per-tier ANTHROPIC_DEFAULT_*_MODEL overrides, keyed by env var name. */
export type TierOverrides = Record<string, string>;

type Props = {
  secret: string;
  modelOptions: ModelOption[];
  overrides: TierOverrides;
  onChange: (next: TierOverrides) => void;
};

const TIER_LABELS: Record<MappingTier, string> = {
  default: "Default",
  fable: "Fable",
  opus: "Opus",
  sonnet: "Sonnet",
  haiku: "Haiku",
};

/**
 * Claude Code writes one env var per model tier. tproxy normally leaves the
 * placeholder names in place and rewrites them server-side (see Mapping), but a
 * per-tier override lets a slot point straight at a virtual model — the behaviour
 * 9router's model-slot table offered.
 */
export function ClaudeCodeOptions({ secret, modelOptions, overrides, onChange }: Props) {
  const { t } = useTranslation();
  const [filterNaming, setFilterNaming] = useState(false);
  const [savingFilter, setSavingFilter] = useState(false);

  useEffect(() => {
    if (!secret) return;
    let cancelled = false;
    void fetchAdminSettings(secret)
      .then((settings) => {
        if (!cancelled) setFilterNaming(Boolean(settings.cc_filter_naming));
      })
      .catch(() => {
        /* settings endpoint may be unavailable during startup */
      });
    return () => {
      cancelled = true;
    };
  }, [secret]);

  const handleFilterToggle = async (next: boolean) => {
    setFilterNaming(next);
    setSavingFilter(true);
    try {
      await saveGatewaySettings(secret, { cc_filter_naming: next });
    } catch {
      setFilterNaming(!next);
    } finally {
      setSavingFilter(false);
    }
  };

  const setTier = (tier: MappingTier, value: string) => {
    const envKey = TIER_ENV_VARS[tier];
    const next = { ...overrides };
    if (value) next[envKey] = value;
    else delete next[envKey];
    onChange(next);
  };

  return (
    <div className="cli-tool-claude-options">
      <div className="cli-tool-field-stack">
        <p className="cli-tool-step-title">{t("cliTools.tierOverrides")}</p>
        <p className="cli-tool-step-desc">
          {t("cliTools.tierOverridesDesc")} <Link to="/mapping">Mapping</Link>
        </p>
        {MAPPING_TIERS.map((tier) => (
          <Field key={tier} label={TIER_LABELS[tier]}>
            <Select
              value={overrides[TIER_ENV_VARS[tier]] ?? ""}
              onChange={(event) => setTier(tier, event.target.value)}
            >
              <option value="">{tier} (placeholder)</option>
              {modelOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </Field>
        ))}
      </div>

      <Toggle
        label={t("cliTools.filterNaming")}
        checked={filterNaming}
        disabled={savingFilter}
        onChange={(event) => void handleFilterToggle(event.target.checked)}
      />
      <p className="cli-tool-hint">{t("cliTools.filterNamingDesc")}</p>
    </div>
  );
}
