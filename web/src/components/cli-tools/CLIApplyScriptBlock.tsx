import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui";
import { applyScriptAvailable, buildApplyScript, type ApplyScriptShell } from "./applyScripts";
import type { ManualConfigEntry } from "./ManualConfigModal";

type Props = {
  configs: ManualConfigEntry[];
  disabled?: boolean;
};

const SHELLS: { id: ApplyScriptShell; label: string }[] = [
  { id: "bash", label: "Bash" },
  { id: "powershell", label: "PowerShell" },
];

export function CLIApplyScriptBlock({ configs, disabled = false }: Props) {
  const { t } = useTranslation();
  const [shell, setShell] = useState<ApplyScriptShell>("bash");
  const [copied, setCopied] = useState(false);

  const script = useMemo(() => buildApplyScript(shell, configs), [shell, configs]);
  const available = applyScriptAvailable(shell, configs);

  const copyScript = async () => {
    if (!script) return;
    try {
      await navigator.clipboard.writeText(script);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  if (configs.length === 0) {
    return null;
  }

  return (
    <div className="cli-tool-apply-script">
      <div className="cli-tool-apply-script-head">
        <div>
          <p className="cli-tool-apply-script-title">{t("cliTools.oneShotCommand")}</p>
          <p className="cli-tool-apply-script-desc">
            {t("cliTools.oneShotCommandDesc")}
          </p>
        </div>
        <div className="cli-tool-apply-script-tabs usage-segmented">
          {SHELLS.map((item) => (
            <button
              key={item.id}
              type="button"
              className={shell === item.id ? "active" : ""}
              onClick={() => setShell(item.id)}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

      {!available ? (
        <p className="cli-tool-hint">
          {shell === "bash"
            ? t("cliTools.windowsOnlyHint")
            : t("cliTools.noScriptAvailable")}
        </p>
      ) : (
        <div className="cli-tool-codeblock">
          <div className="cli-tool-codeblock-head">
            <span>{shell === "bash" ? "bash" : "powershell"}</span>
            <Button
              variant="ghost"
              size="sm"
              className="btn-icon-only"
              icon={copied ? "check" : "content_copy"}
              aria-label={copied ? t("common.copied") : t("common.copy")}
              title={copied ? t("common.copied") : t("common.copy")}
              disabled={disabled || !script}
              onClick={() => void copyScript()}
            />
          </div>
          <pre>
            <code>{script}</code>
          </pre>
        </div>
      )}
    </div>
  );
}
