import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { Tag, ToolButton } from "../page-kit";
import { cn } from "../ui";
import { GLOBAL_ROTATION_STRATEGIES, type ProviderRotationStrategy } from "../providers/api";
import { PanelSkeleton, SettingRow, SettingsPanel } from "./SettingsParts";
import { STRATEGY_ICON } from "./utils";

type Props = {
  loading: boolean;
  dirty: boolean;
  strategy: string;
  onStrategyChange: (value: string) => void;
  stickyLimit: string;
  onStickyLimitChange: (value: string) => void;
  stickyError?: string;
  overrides: Record<string, ProviderRotationStrategy>;
  providerNames: Record<string, string>;
  resetting: boolean;
  onReset: () => void;
};

export function RotationPanel({
  loading,
  dirty,
  strategy,
  onStrategyChange,
  stickyLimit,
  onStickyLimitChange,
  stickyError,
  overrides,
  providerNames,
  resetting,
  onReset,
}: Props) {
  const { t } = useTranslation();
  const overrideIds = Object.keys(overrides).sort();
  const strategyName = (id: string) => t(`settings.strategy.${id}.label`, { defaultValue: id });
  const stickyApplies = strategy === "round-robin";

  return (
    <SettingsPanel
      id="rotation"
      icon="sync_alt"
      title={t("settings.accountRotation")}
      subtitle={t("settings.page.rotationSubtitle")}
      dirty={dirty}
      actions={
        <ToolButton icon="restart_alt" spinning={resetting} disabled={loading || resetting} onClick={onReset}>
          {t("settings.resetRuntimeState")}
        </ToolButton>
      }
    >
      {loading ? (
        <PanelSkeleton rows={3} />
      ) : (
        <>
          <SettingRow label={t("settings.defaultStrategy")} hint={t("settings.page.strategyHint")}>
            <div className="st-choices" role="radiogroup" aria-label={t("settings.defaultStrategy")}>
              {GLOBAL_ROTATION_STRATEGIES.map((item) => {
                const selected = strategy === item.id;
                return (
                  <label key={item.id} className={cn("st-choice", selected && "is-selected")}>
                    <input
                      type="radio"
                      name="st-strategy"
                      value={item.id}
                      checked={selected}
                      onChange={() => onStrategyChange(item.id)}
                    />
                    <span className="material-symbols-outlined st-choice-icon" aria-hidden>
                      {STRATEGY_ICON[item.id] || "sync_alt"}
                    </span>
                    <span className="st-choice-copy">
                      <strong>
                        {strategyName(item.id)}
                        {item.id === "round-robin" ? <Tag tone="muted">{t("settings.page.default")}</Tag> : null}
                      </strong>
                      <small>{t(`settings.strategy.${item.id}.hint`, { defaultValue: item.label })}</small>
                    </span>
                  </label>
                );
              })}
            </div>
          </SettingRow>

          <SettingRow
            label={t("settings.stickyLimit")}
            hint={stickyApplies ? t("settings.page.stickyHint") : t("settings.page.stickyHintInactive")}
            htmlFor="st-sticky"
            error={stickyError}
          >
            <div className={cn("st-number", !stickyApplies && "is-inactive")}>
              <input
                id="st-sticky"
                className="input"
                type="number"
                min={1}
                step={1}
                inputMode="numeric"
                value={stickyLimit}
                aria-invalid={Boolean(stickyError)}
                onChange={(event) => onStickyLimitChange(event.target.value)}
              />
              <span>{t("settings.page.requestsPerAccount")}</span>
            </div>
          </SettingRow>

          <SettingRow label={t("settings.page.overridesLabel")} hint={t("settings.page.overridesHint")}>
            {overrideIds.length === 0 ? (
              <p className="st-muted">{t("settings.page.noOverrides")}</p>
            ) : (
              <ul className="st-overrides">
                {overrideIds.map((id) => {
                  const override = overrides[id];
                  return (
                    <li key={id}>
                      <Link to={`/providers/${encodeURIComponent(id)}`}>{providerNames[id] || id}</Link>
                      <span className="st-mono">
                        {override.strategy ? strategyName(override.strategy) : t("settings.page.inheritsStrategy")}
                        {override.sticky_round_robin_limit ? ` · ${t("settings.page.stickyShort", { count: override.sticky_round_robin_limit })}` : ""}
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
          </SettingRow>
        </>
      )}
    </SettingsPanel>
  );
}
