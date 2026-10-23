import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Button, Field, Input, Select } from "../ui";
import { PageNote, SectionHead, StatusPill, Tag } from "../page-kit";
import {
  ROTATION_STRATEGIES,
  effectiveProviderRotation,
  fetchAccountRotation,
  rotationStrategyLabel,
  saveAccountRotation,
  type AccountRotationSettings,
} from "./api";

type Props = {
  providerId: string;
  providerName: string;
  accountCount: number;
  secret: string;
  onSaved?: (message: string) => void;
  onError?: (message: string) => void;
};

const STRATEGY_KEYS: Record<string, string> = {
  "fill-first": "providers.rotation.strategy.fillFirst",
  "round-robin": "providers.rotation.strategy.roundRobin",
  "weighted-round-robin": "providers.rotation.strategy.weighted",
  "expiring-first": "providers.rotation.strategy.expiringFirst",
};

function strategyText(strategy: string, t: TFunction) {
  const key = STRATEGY_KEYS[strategy];
  return key ? t(key) : rotationStrategyLabel(strategy);
}

/** Per-provider override of the global account rotation strategy. */
export function ProviderRotationCard({ providerId, providerName, accountCount, secret, onSaved, onError }: Props) {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<AccountRotationSettings | null>(null);
  const [strategyOverride, setStrategyOverride] = useState("");
  const [stickyOverride, setStickyOverride] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const hydrate = useCallback(
    (response: AccountRotationSettings) => {
      setSettings(response);
      const override = response.provider_strategies?.[providerId];
      setStrategyOverride(override?.strategy || "");
      setStickyOverride(override?.sticky_round_robin_limit && override.sticky_round_robin_limit > 0 ? String(override.sticky_round_robin_limit) : "");
    },
    [providerId],
  );

  const load = useCallback(async () => {
    setLoading(true);
    try {
      hydrate(await fetchAccountRotation(secret));
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("providers.failedLoadRotation"));
    } finally {
      setLoading(false);
    }
    // t is left out so a language switch does not refetch.
  }, [hydrate, onError, secret]);

  useEffect(() => {
    void load();
  }, [load]);

  const effective = useMemo(
    () => (settings ? effectiveProviderRotation(providerId, settings) : { strategy: "round-robin", stickyRoundRobinLimit: 3, isOverride: false }),
    [providerId, settings],
  );

  const saved = settings?.provider_strategies?.[providerId];
  const savedStrategy = saved?.strategy || "";
  const savedSticky = saved?.sticky_round_robin_limit && saved.sticky_round_robin_limit > 0 ? String(saved.sticky_round_robin_limit) : "";
  const dirty = settings !== null && (strategyOverride !== savedStrategy || stickyOverride.trim() !== savedSticky);
  const stickyDisabled = strategyOverride === "fill-first" || (!strategyOverride && settings?.strategy === "fill-first");

  const persist = async (nextStrategies: AccountRotationSettings["provider_strategies"], message: string, failure: string) => {
    if (!settings) return;
    setSaving(true);
    try {
      const response = await saveAccountRotation(secret, {
        strategy: settings.strategy,
        sticky_round_robin_limit: settings.sticky_round_robin_limit,
        provider_strategies: nextStrategies,
      });
      hydrate(response);
      onSaved?.(message);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : failure);
    } finally {
      setSaving(false);
    }
  };

  const save = () => {
    if (!settings) return;
    const next = { ...(settings.provider_strategies || {}) };
    const stickyValue = stickyOverride.trim() ? Number(stickyOverride) : undefined;
    if (!strategyOverride && !stickyValue) {
      delete next[providerId];
    } else {
      next[providerId] = {
        ...(strategyOverride ? { strategy: strategyOverride } : {}),
        ...(stickyValue && stickyValue > 0 ? { sticky_round_robin_limit: stickyValue } : {}),
      };
    }
    void persist(next, t("providers.rotationSavedFor", { name: providerName }), t("providers.failedSaveRotation"));
  };

  const clearOverride = () => {
    if (!settings) return;
    const next = { ...(settings.provider_strategies || {}) };
    delete next[providerId];
    void persist(next, t("providers.rotationUsingGlobal", { name: providerName }), t("providers.failedClearRotation"));
  };

  return (
    <section className="cp-panel pv-panel pv-rotation" aria-label={t("providers.rotationAccountRotation")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>autorenew</span>}
        title={t("providers.rotationAccountRotation")}
        subtitle={t("providers.rotation.subtitle")}
        meta={
          settings ? (
            effective.isOverride ? (
              <StatusPill tone="info">{t("providers.rotationProviderOverride")}</StatusPill>
            ) : (
              <StatusPill tone="neutral">{t("providers.rotationGlobalDefault")}</StatusPill>
            )
          ) : null
        }
      />

      {loading || !settings ? (
        <div className="cp-skeleton pv-rotation-skeleton" aria-label={t("providers.rotationLoadingSettings")}>
          <span />
          <span />
        </div>
      ) : (
        <div className="pv-rotation-body">
          <dl className="cp-facts pv-rotation-facts">
            <div>
              <dt>{t("providers.rotation.effective")}</dt>
              <dd title={strategyText(effective.strategy, t)}>{strategyText(effective.strategy, t)}</dd>
            </div>
            <div>
              <dt>{t("providers.rotation.stickyLimit")}</dt>
              <dd>{effective.strategy === "round-robin" ? effective.stickyRoundRobinLimit : "—"}</dd>
            </div>
            <div>
              <dt>{t("providers.rotation.globalDefault")}</dt>
              <dd title={strategyText(settings.strategy, t)}>
                {strategyText(settings.strategy, t)}
                {settings.strategy === "round-robin" ? ` · ${settings.sticky_round_robin_limit}` : ""}
              </dd>
            </div>
          </dl>

          <div className="pv-rotation-form">
            <Field label={t("providers.rotationStrategyOverride")}>
              <Select value={strategyOverride} onChange={(event) => setStrategyOverride(event.target.value)}>
                {ROTATION_STRATEGIES.map((strategy) => (
                  <option key={strategy.id || "global"} value={strategy.id}>
                    {strategy.id === ""
                      ? t("providers.rotation.useGlobal", { strategy: strategyText(settings.strategy, t) })
                      : strategyText(strategy.id, t)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("providers.rotationStickyLimitOverride")} hint={stickyDisabled ? t("providers.rotation.stickyDisabled") : undefined}>
              <Input
                type="number"
                min={1}
                max={100}
                placeholder={t("providers.rotation.globalValue", { value: settings.sticky_round_robin_limit })}
                value={stickyOverride}
                onChange={(event) => setStickyOverride(event.target.value)}
                disabled={stickyDisabled}
              />
            </Field>
          </div>

          <PageNote icon="info">
            {t("providers.rotationFailedAccountsSkipped")}
            {accountCount < 2 && effective.strategy === "round-robin" ? ` ${t("providers.rotationSingleAccountHint")}` : ""}
          </PageNote>

          <div className="pv-rotation-actions">
            {dirty ? <Tag tone="warning">{t("providers.rotation.unsaved")}</Tag> : null}
            {effective.isOverride ? (
              <Button variant="ghost" size="sm" disabled={saving} onClick={clearOverride}>
                {t("providers.rotationUseGlobalDefault")}
              </Button>
            ) : null}
            <Button variant="primary" size="sm" icon="save" loading={saving} disabled={!dirty || saving} onClick={save}>
              {t("providers.rotation.save")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
