import { useMemo, useState } from "react";
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
          <p className="cli-tool-apply-script-title">One-shot command</p>
          <p className="cli-tool-apply-script-desc">
            Copy and paste into your terminal to run immediately.
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
            ? "This tool uses Windows-only paths. Switch to the PowerShell tab."
            : "No script available for the current configuration."}
        </p>
      ) : (
        <div className="cli-tool-codeblock">
          <div className="cli-tool-codeblock-head">
            <span>{shell === "bash" ? "bash" : "powershell"}</span>
            <Button
              variant="ghost"
              size="sm"
              icon={copied ? "check" : "content_copy"}
              disabled={disabled || !script}
              onClick={() => void copyScript()}
            >
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
          <pre>
            <code>{script}</code>
          </pre>
        </div>
      )}
    </div>
  );
}
