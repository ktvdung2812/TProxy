import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { LinkButton, SectionHead, ToolButton } from "../page-kit";
import { ClientTag } from "./ComboItem";
import type { ComboPreset } from "./types";

/** CLI tool pages that explain how to point each client at a combo ID. */
export const CLIENT_SETUP_ROUTES: Partial<Record<ComboPreset["client"], string>> = {
  "claude-code": "/cli-tools/claude",
  cowork: "/cli-tools/cowork",
};

type Props = {
  presets: ComboPreset[];
  onUse: (preset: ComboPreset) => void;
};

/** Ready-made fallback profiles; one click opens the form pre-filled. */
export function ComboPresets({ presets, onUse }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <section className="cb-group" aria-label={t("combos.quickPresets")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>auto_awesome</span>}
        title={t("combos.quickPresets")}
        subtitle={t("combos.quickPresetsDesc")}
      />
      <div className="cp-grid cb-presets">
        {presets.map((preset) => {
          const setup = CLIENT_SETUP_ROUTES[preset.client];
          const empty = preset.modelIds.length === 0;
          return (
            <article key={preset.id} className="cp-card cb-preset">
              <header className="cp-card-head">
                <div className="cp-card-title">
                  <strong>{t(`combos.presets.${preset.id}.title`, { defaultValue: preset.title })}</strong>
                  <div className="cp-card-tags">
                    <ClientTag client={preset.client} />
                    <code className="cb-id">{preset.comboId}</code>
                  </div>
                </div>
              </header>
              <div className="cp-card-body">
                <p className="cb-preset-desc">{t(`combos.presets.${preset.id}.description`, { defaultValue: preset.description })}</p>
                {empty ? (
                  <p className="cb-preset-models is-empty">{t("combos.noVirtualModels")}</p>
                ) : (
                  <p className="cb-preset-models" title={preset.modelIds.join(" → ")}>
                    {preset.modelIds.map((modelId, index) => (
                      <span key={modelId}>
                        {index > 0 ? <span className="cb-preset-arrow" aria-hidden>→</span> : null}
                        <code>{modelId}</code>
                      </span>
                    ))}
                  </p>
                )}
              </div>
              <footer className="cp-card-foot">
                <div className="cp-card-meta">
                  {setup ? (
                    <LinkButton onClick={() => navigate(setup)}>{t("combos.setupGuide")}</LinkButton>
                  ) : (
                    <span>{t("combos.anyClient")}</span>
                  )}
                </div>
                <ToolButton icon="auto_awesome" disabled={empty} onClick={() => onUse(preset)} title={empty ? t("combos.noPresetError") : undefined}>
                  {t("combos.usePreset")}
                </ToolButton>
              </footer>
            </article>
          );
        })}
      </div>
    </section>
  );
}
