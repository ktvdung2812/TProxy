import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge, Button, Card } from "../ui";
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

export function ProviderRotationCard({
  providerId,
  providerName,
  accountCount,
  secret,
  onSaved,
  onError,
}: Props) {
  const [settings, setSettings] = useState<AccountRotationSettings | null>(null);
  const [strategyOverride, setStrategyOverride] = useState("");
  const [stickyOverride, setStickyOverride] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetchAccountRotation(secret);
      setSettings(response);
      const override = response.provider_strategies?.[providerId];
      setStrategyOverride(override?.strategy || "");
      setStickyOverride(
        override?.sticky_round_robin_limit && override.sticky_round_robin_limit > 0
          ? String(override.sticky_round_robin_limit)
          : "",
      );
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to load rotation settings");
    } finally {
      setLoading(false);
    }
  }, [onError, providerId, secret]);

  useEffect(() => {
    void load();
  }, [load]);

  const effective = useMemo(() => {
    if (!settings) {
      return { strategy: "round-robin", stickyRoundRobinLimit: 3, isOverride: false };
    }
    return effectiveProviderRotation(providerId, settings);
  }, [providerId, settings]);

  const save = async () => {
    if (!settings) return;
    setSaving(true);
    try {
      const nextStrategies = { ...(settings.provider_strategies || {}) };
      const stickyValue = stickyOverride.trim() ? Number(stickyOverride) : undefined;
      if (!strategyOverride && !stickyValue) {
        delete nextStrategies[providerId];
      } else {
        nextStrategies[providerId] = {
          ...(strategyOverride ? { strategy: strategyOverride } : {}),
          ...(stickyValue && stickyValue > 0 ? { sticky_round_robin_limit: stickyValue } : {}),
        };
      }
      const payload: AccountRotationSettings = {
        strategy: settings.strategy,
        sticky_round_robin_limit: settings.sticky_round_robin_limit,
        provider_strategies: nextStrategies,
      };
      const saved = await saveAccountRotation(secret, payload);
      setSettings(saved);
      onSaved?.(`Rotation settings saved for ${providerName}`);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to save rotation settings");
    } finally {
      setSaving(false);
    }
  };

  const clearOverride = async () => {
    if (!settings) return;
    setSaving(true);
    try {
      const nextStrategies = { ...(settings.provider_strategies || {}) };
      delete nextStrategies[providerId];
      const saved = await saveAccountRotation(secret, {
        strategy: settings.strategy,
        sticky_round_robin_limit: settings.sticky_round_robin_limit,
        provider_strategies: nextStrategies,
      });
      setSettings(saved);
      setStrategyOverride("");
      setStickyOverride("");
      onSaved?.(`Using global rotation defaults for ${providerName}`);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to clear rotation override");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card
      pad="md"
      className="section provider-rotation-card"
      title="Account rotation"
      icon="autorenew"
      action={
        effective.isOverride ? (
          <Badge variant="info" size="sm">Provider override</Badge>
        ) : (
          <Badge variant="default" size="sm">Global default</Badge>
        )
      }
    >
      {loading || !settings ? (
        <p className="muted">Loading rotation settings…</p>
      ) : (
        <>
          <p className="muted provider-rotation-summary">
            Effective for <strong>{providerName}</strong>:{" "}
            <code>{rotationStrategyLabel(effective.strategy)}</code>
            {effective.strategy === "round-robin" && (
              <>
                {" "}
                · sticky limit <code>{effective.stickyRoundRobinLimit}</code>
              </>
            )}
            {accountCount < 2 && effective.strategy === "round-robin" && (
              <span className="provider-rotation-hint">
                {" "}
                Single-account providers still track sticky rotation state for 9router parity.
              </span>
            )}
          </p>

          <div className="model-form-grid provider-rotation-form">
            <label>
              Strategy override
              <select value={strategyOverride} onChange={(event) => setStrategyOverride(event.target.value)}>
                {ROTATION_STRATEGIES.map((strategy) => (
                  <option key={strategy.id || "global"} value={strategy.id}>
                    {strategy.id === ""
                      ? `Global (${rotationStrategyLabel(settings.strategy)})`
                      : strategy.label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Sticky limit override
              <input
                type="number"
                min={1}
                max={100}
                placeholder={`Global (${settings.sticky_round_robin_limit})`}
                value={stickyOverride}
                onChange={(event) => setStickyOverride(event.target.value)}
                disabled={strategyOverride === "fill-first" || (!strategyOverride && settings.strategy === "fill-first")}
              />
            </label>
          </div>

          <p className="muted" style={{ fontSize: 13, marginTop: 8 }}>
            Global defaults: {rotationStrategyLabel(settings.strategy)}
            {settings.strategy === "round-robin" && ` · sticky ${settings.sticky_round_robin_limit}`}.
            Failed accounts are skipped automatically; cooldowns apply per model when upstream rate-limits.
          </p>

          <div className="actions-row" style={{ marginTop: 12 }}>
            <Button variant="primary" size="sm" icon="save" loading={saving} onClick={() => void save()}>
              Save provider rotation
            </Button>
            {effective.isOverride && (
              <Button variant="ghost" size="sm" disabled={saving} onClick={() => void clearOverride()}>
                Use global default
              </Button>
            )}
          </div>
        </>
      )}
    </Card>
  );
}
