import { useCallback, useEffect, useState } from "react";
import { Badge, Button, Card } from "../ui";
import {
  GLOBAL_ROTATION_STRATEGIES,
  fetchAccountRotation,
  resetAccountRotationState,
  rotationStrategyLabel,
  saveAccountRotation,
  type AccountRotationSettings,
} from "./api";

type Props = {
  secret: string;
  onSaved?: (message: string) => void;
  onError?: (message: string) => void;
};

/** Global account rotation defaults — mirrors 9router profile settings. */
export function GlobalAccountRotationCard({ secret, onSaved, onError }: Props) {
  const [settings, setSettings] = useState<AccountRotationSettings | null>(null);
  const [strategy, setStrategy] = useState("round-robin");
  const [stickyLimit, setStickyLimit] = useState("3");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetchAccountRotation(secret);
      setSettings(response);
      setStrategy(response.strategy || "round-robin");
      setStickyLimit(String(response.sticky_round_robin_limit || 3));
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to load rotation settings");
    } finally {
      setLoading(false);
    }
  }, [onError, secret]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    if (!settings) return;
    setSaving(true);
    try {
      const stickyValue = Number(stickyLimit);
      const saved = await saveAccountRotation(secret, {
        strategy,
        sticky_round_robin_limit: stickyValue > 0 ? stickyValue : 3,
        provider_strategies: settings.provider_strategies || {},
      });
      setSettings(saved);
      onSaved?.("Global account rotation saved");
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to save rotation settings");
    } finally {
      setSaving(false);
    }
  };

  const resetAll = async () => {
    setResetting(true);
    try {
      await resetAccountRotationState(secret);
      onSaved?.("Rotation counters reset for all providers");
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to reset rotation state");
    } finally {
      setResetting(false);
    }
  };

  const overrideCount = Object.keys(settings?.provider_strategies || {}).length;

  return (
    <Card
      pad="md"
      className="section global-rotation-card"
      title="Global account rotation"
      icon="autorenew"
      action={<Badge variant="info" size="sm">9router-compatible</Badge>}
    >
      {loading || !settings ? (
        <p className="muted">Loading rotation settings…</p>
      ) : (
        <>
          <p className="muted global-rotation-summary">
            Controls how credentials are picked when a provider has multiple accounts.
            Sticky round-robin keeps the same account for N requests, then rotates to the least recently used.
            {overrideCount > 0 && (
              <span>
                {" "}
                <strong>{overrideCount}</strong> provider{overrideCount === 1 ? "" : "s"} use custom overrides.
              </span>
            )}
          </p>

          <div className="model-form-grid global-rotation-form">
            <label>
              Default strategy
              <select value={strategy} onChange={(event) => setStrategy(event.target.value)}>
                {GLOBAL_ROTATION_STRATEGIES.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Sticky round-robin limit
              <input
                type="number"
                min={1}
                max={100}
                value={stickyLimit}
                disabled={strategy !== "round-robin"}
                onChange={(event) => setStickyLimit(event.target.value)}
              />
            </label>
          </div>

          <p className="muted" style={{ fontSize: 13, marginTop: 8 }}>
            Effective default: <code>{rotationStrategyLabel(strategy)}</code>
            {strategy === "round-robin" && (
              <>
                {" "}
                · sticky <code>{stickyLimit}</code>
              </>
            )}
            . Settings persist in the database and survive restarts. Import from 9router restores strategy and per-provider overrides.
          </p>

          <div className="actions-row" style={{ marginTop: 12 }}>
            <Button variant="primary" size="sm" icon="save" loading={saving} onClick={() => void save()}>
              Save global rotation
            </Button>
            <Button variant="ghost" size="sm" loading={resetting} onClick={() => void resetAll()}>
              Reset rotation counters
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}
