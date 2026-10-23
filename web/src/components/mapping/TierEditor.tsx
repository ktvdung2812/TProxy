import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Select, cn } from "../ui";
import { ActionMenu, EmptyPanel, SectionHead, StatusPill, Tag, ToneCounts, type ActionMenuItem, type Tone } from "../page-kit";
import type { ModelOption } from "../../lib/modelOptions";
import type { ClaudeMappingResponse } from "./api";
import {
  CLAUDE_MAPPING_PLACEHOLDER_NAMES,
  CODEX_MAPPING_PLACEHOLDER_NAMES,
  GPT_CLIENT_TIER_PLACEHOLDERS,
  TIER_ENV_VARS,
  reasoningEffortOptionsForTarget,
  type MappingTier,
} from "./codegen";
import { ModelTargetCombobox } from "./ModelTargetCombobox";
import type { MappingEditor } from "./useMappingEditor";

export type TierClient = "claude" | "codex";

type TierDef = { id: MappingTier; label: string; aliases: string; sharedWith?: string };

export function tierDefs(client: TierClient, t: TFunction): TierDef[] {
  const claude: TierDef[] = [
    { id: "default", label: t("mapping.default"), aliases: "default" },
    { id: "fable", label: t("mapping.claudeFable"), aliases: "claude-fable, fable" },
    { id: "opus", label: t("mapping.claudeOpus"), aliases: "claude-opus, opus, opusplan" },
    { id: "sonnet", label: t("mapping.claudeSonnet"), aliases: "claude-sonnet, sonnet" },
    { id: "haiku", label: t("mapping.claudeHaiku"), aliases: "claude-haiku, haiku" },
  ];
  if (client === "claude") return claude;
  // Codex codenames are aliases of the Claude tiers, so they edit the same target.
  const names: Partial<Record<MappingTier, string>> = { fable: "GPT Sol", opus: "GPT Terra", haiku: "GPT Luna" };
  return claude
    .filter((tier) => GPT_CLIENT_TIER_PLACEHOLDERS[tier.id])
    .map((tier) => ({ id: tier.id, label: names[tier.id] || tier.id, aliases: GPT_CLIENT_TIER_PLACEHOLDERS[tier.id] || "", sharedWith: tier.label }));
}

export type TierStatus = "changed" | "override" | "env" | "default";
export const TIER_STATUS_TONE: Record<TierStatus, Tone> = { changed: "warning", override: "primary", env: "info", default: "neutral" };

const ROUTE_TONE: Record<string, Tone> = { "virtual-model": "success", "claude-native": "info", "codex-bridge": "primary" };
const ROUTE_LABEL: Record<string, string> = {
  "virtual-model": "mapping.route.virtualModel",
  "claude-native": "mapping.route.claudeNative",
  "codex-bridge": "mapping.route.codexBridge",
};

export function tierStatus(editor: MappingEditor, tier: MappingTier): TierStatus {
  if (editor.tiers.changed.includes(tier)) return "changed";
  if (editor.tiers.saved[tier]) return "override";
  if (editor.data?.env_defaults?.[tier]) return "env";
  return "default";
}

type Props = {
  client: TierClient;
  editor: MappingEditor;
  options: ModelOption[];
  search: string;
  formatTarget: (target: string) => string;
  onCopy: (text: string, label: string) => void;
  onClearSearch: () => void;
};

export function TierEditor({ client, editor, options, search, formatTarget, onCopy, onClearSearch }: Props) {
  const { t } = useTranslation();
  const data = editor.data;
  const query = search.trim().toLowerCase();
  const all = tierDefs(client, t);
  const visible = query
    ? all.filter((tier) =>
        [tier.label, tier.aliases, tier.id, editor.tiers.overrides[tier.id], data?.effective_resolved?.[tier.id]?.resolved]
          .some((value) => value?.toLowerCase().includes(query)),
      )
    : all;
  const counts = { changed: 0, override: 0, env: 0, default: 0 } as Record<TierStatus, number>;
  for (const tier of all) counts[tierStatus(editor, tier.id)] += 1;

  return (
    <section className="cp-panel mp-panel">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>{client === "claude" ? "swap_horiz" : "terminal"}</span>}
        title={client === "claude" ? t("mapping.tierPanel.claudeTitle") : t("mapping.tierPanel.codexTitle")}
        subtitle={client === "claude" ? t("mapping.tierPanel.claudeSubtitle") : t("mapping.tierPanel.codexSubtitle")}
        meta={
          <ToneCounts
            items={[
              { tone: "warning", count: counts.changed, label: t("mapping.status.changed") },
              { tone: "primary", count: counts.override, label: t("mapping.status.override") },
              { tone: "info", count: counts.env, label: t("mapping.status.env") },
            ]}
          />
        }
      />
      {visible.length === 0 ? (
        <EmptyPanel
          compact
          icon="filter_alt_off"
          title={t("mapping.noMatch")}
          description={t("mapping.noMatchHint")}
          action={<button type="button" className="cp-link-btn" onClick={onClearSearch}>{t("mapping.clearSearch")}</button>}
        />
      ) : (
        <div className="mp-rows" role="list">
          {visible.map((tier) => (
            <TierRow key={tier.id} tier={tier} client={client} editor={editor} options={options} formatTarget={formatTarget} onCopy={onCopy} />
          ))}
        </div>
      )}
    </section>
  );
}

function TierRow({
  tier,
  client,
  editor,
  options,
  formatTarget,
  onCopy,
}: {
  tier: TierDef;
  client: TierClient;
  editor: MappingEditor;
  options: ModelOption[];
  formatTarget: (target: string) => string;
  onCopy: (text: string, label: string) => void;
}) {
  const { t } = useTranslation();
  const data = editor.data;
  const status = tierStatus(editor, tier.id);
  const resolved = data?.effective_resolved?.[tier.id];
  const routed = resolved?.resolved || data?.effective?.[tier.id] || "";
  const override = editor.tiers.overrides[tier.id] || "";
  const effort = editor.tiers.efforts[tier.id] || "";
  const savedEffort = data?.effective_reasoning_effort?.[tier.id];
  const envDefault = data?.env_defaults?.[tier.id];
  const codename = GPT_CLIENT_TIER_PLACEHOLDERS[tier.id];

  const menu: ActionMenuItem[] = [
    status === "changed" && {
      key: "revert",
      icon: "undo",
      label: t("mapping.action.revert"),
      hint: t("mapping.action.revertHint"),
      onSelect: () => editor.tiers.revert(tier.id),
    },
    {
      key: "reset",
      icon: "restart_alt",
      label: t("mapping.action.resetTier"),
      hint: data?.defaults?.[tier.id] ? t("mapping.action.resetTierHint", { target: envDefault || data.defaults[tier.id] }) : undefined,
      disabled: !override && !effort,
      onSelect: () => editor.tiers.reset(tier.id),
    },
    routed && { key: "copy-target", icon: "content_copy", label: t("mapping.action.copyRouted"), hint: routed, onSelect: () => onCopy(routed, routed) },
    client === "codex" && codename
      ? { key: "copy-codename", icon: "content_copy", label: t("mapping.action.copyCodename"), hint: codename, onSelect: () => onCopy(codename, codename) }
      : { key: "copy-env", icon: "data_object", label: t("mapping.action.copyEnvVar"), hint: TIER_ENV_VARS[tier.id], onSelect: () => onCopy(TIER_ENV_VARS[tier.id], TIER_ENV_VARS[tier.id]) },
  ].filter(Boolean) as ActionMenuItem[];

  return (
    <div className={cn("mp-row mp-tier-row", `mp-state-${status}`)} role="listitem">
      <div className="mp-row-source">
        <div className="mp-row-title">
          <strong>{tier.label}</strong>
          <StatusPill tone={TIER_STATUS_TONE[status]} title={t(`mapping.statusHint.${status}`)}>{t(`mapping.status.${status}`)}</StatusPill>
        </div>
        <code className="mp-row-id" title={t("mapping.aliasesHint")}>{tier.aliases}</code>
        <div className="mp-row-tags">
          {tier.sharedWith ? <Tag tone="neutral" title={t("mapping.sharedWithHint")}>{t("mapping.sharedWith", { tier: tier.sharedWith })}</Tag> : null}
          {resolved?.route ? <Tag tone={ROUTE_TONE[resolved.route] || "neutral"}>{t(ROUTE_LABEL[resolved.route] || "mapping.route.codexBridge")}</Tag> : null}
          {savedEffort ? <Tag tone="info">{t("mapping.effortTag", { effort: savedEffort })}</Tag> : null}
          {envDefault ? <Tag tone="neutral" title={TIER_ENV_VARS[tier.id]}>{t("mapping.envTag", { target: envDefault })}</Tag> : null}
        </div>
      </div>
      <span className="mp-row-arrow material-symbols-outlined" aria-hidden>arrow_forward</span>
      <div className="mp-row-target">
        <div className="mp-tier-controls">
          <ModelTargetCombobox
            aria-label={t("mapping.targetFor", { name: tier.label })}
            value={override}
            placeholder={data?.defaults?.[tier.id] || "provider:model"}
            options={options}
            onChange={(next) => editor.tiers.setTarget(tier.id, next)}
          />
          <Select
            className="mp-effort"
            aria-label={t("mapping.effortFor", { name: tier.label })}
            title={t("mapping.effortLabel")}
            value={effort}
            onChange={(event) => editor.tiers.setEffort(tier.id, event.target.value as typeof effort)}
          >
            {reasoningEffortOptionsForTarget(override || resolved?.resolved || "", effort).map((option) => (
              <option key={option.value || "default"} value={option.value}>
                {t(`mapping.effort.${option.value || "default"}`)}
              </option>
            ))}
          </Select>
        </div>
        {routed ? (
          <small className="mp-row-routed" title={routed}>
            {t("mapping.routesNow")} <code>{formatTarget(routed)}</code>
          </small>
        ) : null}
      </div>
      <div className="mp-row-actions">
        <ActionMenu label={t("mapping.moreActions")} items={menu} />
      </div>
    </div>
  );
}

/** What the server rewrites right now — reflects the saved map, not unsaved edits. */
export function PlaceholderPanel({ client, data, formatTarget }: { client: TierClient; data: ClaudeMappingResponse | null; formatTarget: (target: string) => string }) {
  const { t } = useTranslation();
  const order: readonly string[] = client === "claude" ? CLAUDE_MAPPING_PLACEHOLDER_NAMES : CODEX_MAPPING_PLACEHOLDER_NAMES;
  const rank = new Map(order.map((name, index) => [name, index]));
  const items = (data?.placeholders || [])
    .filter((item) => rank.has(item.name.toLowerCase()))
    .sort((left, right) => (rank.get(left.name.toLowerCase()) ?? 0) - (rank.get(right.name.toLowerCase()) ?? 0));
  return (
    <section className="cp-panel mp-panel mp-side">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>list_alt</span>}
        title={t("mapping.placeholderRewrite")}
        subtitle={client === "claude" ? t("mapping.livePlaceholderClaude") : t("mapping.livePlaceholderCodex")}
      />
      {items.length === 0 ? (
        <EmptyPanel compact icon="list_alt" title={t("mapping.noPlaceholders")} description={t("mapping.noPlaceholdersHint")} />
      ) : (
        <ul className="mp-rewrites">
          {items.map((item) => (
            <li key={item.name}>
              <code>{item.name}</code>
              <span className="material-symbols-outlined" aria-hidden>arrow_forward</span>
              <code className={cn(!item.resolves && "is-empty")} title={item.resolves || undefined}>
                {item.resolves ? formatTarget(item.resolves) : "—"}
              </code>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
