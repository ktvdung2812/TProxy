import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import type { CLITool } from "../../cli-tools/constants";
import type { ModelOption } from "../../lib/modelOptions";
import { Select } from "../ui";
import { CopyField, Note } from "./CopyBits";
import { ToolNotes } from "./ToolNotes";

/**
 * MITM tools (Antigravity, Kiro) are listed for 9Router parity only: tproxy does not
 * intercept their traffic. Show the slots they expect and point at Providers instead.
 */
export function MitmToolGuide({ tool, models }: { tool: CLITool; models: ModelOption[] }) {
  const { t } = useTranslation();
  const [model, setModel] = useState(() => models[0]?.value ?? "");
  const firstModel = models[0]?.value ?? "";
  useEffect(() => {
    if (!model && firstModel) setModel(firstModel);
  }, [model, firstModel]);

  return (
    <>
      <Note tone="warning" icon="block">
        <p>{t("cliTools.mitmNotImplemented")}</p>
        <div className="ct-note-actions">
          <Link to="/providers" className="ct-link">
            {t("cliTools.mitm.openProviders")}
            <span className="material-symbols-outlined" aria-hidden>arrow_forward</span>
          </Link>
        </div>
      </Note>
      <ToolNotes tool={tool} />
      <div className="ct-setup">
        <section className="cp-panel ct-setup-main">
          <header className="ct-panel-head">
            <h3>{t("cliTools.modelSlots")}</h3>
          </header>
          <div className="ct-panel-body">
            <p className="ct-step-desc">{t("cliTools.modelSlotsDesc")}</p>
            {tool.defaultModels?.length ? (
              <ul className="ct-slots">
                {tool.defaultModels.map((slot) => (
                  <li key={slot.id}>
                    <code>{slot.id}</code>
                    <span className="material-symbols-outlined" aria-hidden>arrow_forward</span>
                    <span>{slot.name}</span>
                  </li>
                ))}
              </ul>
            ) : null}
            <label className="ct-copy-label" htmlFor={`ct-mitm-model-${tool.id}`}>
              {t("cliTools.virtualModel")}
            </label>
            <Select id={`ct-mitm-model-${tool.id}`} value={model} onChange={(event) => setModel(event.target.value)}>
              {models.length === 0 ? (
                <option value="">{t("cliTools.noModelsConfigured")}</option>
              ) : (
                models.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))
              )}
            </Select>
          </div>
        </section>
        <section className="cp-panel ct-setup-side">
          <header className="ct-panel-head">
            <h3>{t("cliTools.mitm.domain")}</h3>
          </header>
          <div className="ct-panel-body">
            {tool.mitmDomain ? <CopyField value={tool.mitmDomain} copyLabel={t("cliTools.copyDomain")} /> : null}
            <p className="ct-hint">{t("cliTools.mitm.domainHint")}</p>
          </div>
        </section>
      </div>
    </>
  );
}
