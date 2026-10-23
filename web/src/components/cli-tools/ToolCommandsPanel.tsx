import { useTranslation } from "react-i18next";
import type { CLITool, CLIToolGuideStep } from "../../cli-tools/constants";
import { CLIApplyScriptBlock } from "./CLIApplyScriptBlock";
import { CodeBlock, CopyAction, CopyField } from "./CopyBits";
import type { ManualConfigEntry } from "./ManualConfigModal";
import { useToolCopy } from "./toolCopy";

type Props = {
  tool: CLITool;
  /** Guide steps that happen inside the tool itself (install, open settings…). */
  steps: CLIToolGuideStep[];
  commandPreview: string | null;
  manualConfigs: ManualConfigEntry[];
  defaultCommand: string;
  /** Scripts embed the key, so copying waits until a key and model are chosen. */
  ready: boolean;
};

/** Install commands in the catalog are shell lines; give those a copy button. */
const SHELL_COMMAND = /^(npm|npx|curl|pip|pipx|brew|go|cargo) /;

/** Right-hand column: what to do in the tool, the generated config and copy-paste scripts. */
export function ToolCommandsPanel({ tool, steps, commandPreview, manualConfigs, defaultCommand, ready }: Props) {
  const { t } = useTranslation();
  const toolCopy = useToolCopy();

  return (
    <div className="ct-commands">
      {steps.length > 0 ? (
        <div className="ct-block">
          <div className="ct-block-head">
            <div>
              <strong>{t("cliTools.output.inTool", { name: tool.name })}</strong>
              <p>{t("cliTools.output.inToolHint")}</p>
            </div>
          </div>
          <ol className="ct-checklist">
            {steps.map((step) => {
              const desc = toolCopy.stepDesc(tool, step);
              const isCommand = Boolean(step.desc && SHELL_COMMAND.test(step.desc));
              return (
                <li key={step.step}>
                  <strong>{toolCopy.stepTitle(tool, step)}</strong>
                  {isCommand && step.desc ? (
                    <span className="ct-inline-command">
                      <code>{step.desc}</code>
                      <CopyAction text={step.desc} bordered={false} />
                    </span>
                  ) : desc ? (
                    <p>{desc}</p>
                  ) : null}
                </li>
              );
            })}
          </ol>
        </div>
      ) : null}

      {commandPreview ? (
        <div className="ct-block">
          <div className="ct-block-head">
            <div>
              <strong>{t("cliTools.output.preview")}</strong>
              <p>{t("cliTools.output.previewHint")}</p>
            </div>
          </div>
          <CodeBlock language={tool.codeBlock?.language ?? "bash"} code={commandPreview} maxHeight={420} />
        </div>
      ) : null}

      {manualConfigs.length > 0 ? <CLIApplyScriptBlock configs={manualConfigs} disabled={!ready} /> : null}

      {defaultCommand ? (
        <div className="ct-block">
          <div className="ct-block-head">
            <div>
              <strong>{t("cliTools.output.run")}</strong>
              <p>{t("cliTools.output.runHint", { name: tool.name })}</p>
            </div>
          </div>
          <CopyField value={defaultCommand} />
        </div>
      ) : null}
    </div>
  );
}
