import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Segmented, useStoredState } from "../page-kit";
import { applyScriptAvailable, buildApplyScript, type ApplyScriptShell } from "./applyScripts";
import { CodeBlock } from "./CopyBits";
import type { ManualConfigEntry } from "./ManualConfigModal";

type Props = {
  configs: ManualConfigEntry[];
  disabled?: boolean;
};

const SHELLS = ["bash", "powershell"] as const;

/** Bash/PowerShell script that writes the same files the dashboard's Apply writes — for other machines. */
export function CLIApplyScriptBlock({ configs, disabled = false }: Props) {
  const { t } = useTranslation();
  const [shell, setShell] = useStoredState<ApplyScriptShell>("tproxy.cliTools.shell", "bash", SHELLS);

  const script = useMemo(() => buildApplyScript(shell, configs), [shell, configs]);
  const available = applyScriptAvailable(shell, configs);

  if (configs.length === 0) {
    return null;
  }

  return (
    <div className="ct-block">
      <div className="ct-block-head">
        <div>
          <strong>{t("cliTools.oneShotCommand")}</strong>
          <p>{t("cliTools.oneShotCommandDesc")}</p>
        </div>
        <Segmented
          label={t("cliTools.script.shell")}
          value={shell}
          onChange={setShell}
          items={[
            { value: "bash", label: "Bash" },
            { value: "powershell", label: "PowerShell" },
          ]}
        />
      </div>

      {!available || !script ? (
        <p className="ct-hint">{shell === "bash" ? t("cliTools.windowsOnlyHint") : t("cliTools.noScriptAvailable")}</p>
      ) : (
        <CodeBlock language={shell} code={script} disabled={disabled} maxHeight={360} />
      )}
      {disabled && available ? <p className="ct-hint">{t("cliTools.script.needsKey")}</p> : null}
    </div>
  );
}
