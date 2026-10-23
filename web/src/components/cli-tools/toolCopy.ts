import { useTranslation } from "react-i18next";
import type { CLITool, CLIToolGuideStep, CLIToolNote } from "../../cli-tools/constants";

/**
 * Localised catalog text. The tool catalog (cli-tools/constants.ts) is shared
 * with the generators and stays in English; its prose is translated through
 * `cliTools.catalog.<toolId>.*` keys, falling back to the catalog text.
 */
export function useToolCopy() {
  const { t } = useTranslation();
  const key = (toolId: string, path: string) => `cliTools.catalog.${toolId}.${path}`;
  return {
    description: (tool: CLITool) => t(key(tool.id, "description"), { defaultValue: tool.description }),
    note: (tool: CLITool, note: CLIToolNote) =>
      t(key(tool.id, `notes.${(tool.notes ?? []).indexOf(note)}`), { defaultValue: note.text }),
    stepTitle: (tool: CLITool, step: CLIToolGuideStep) =>
      t(key(tool.id, `steps.${step.step}.title`), { defaultValue: step.title }),
    stepDesc: (tool: CLITool, step: CLIToolGuideStep) =>
      step.desc ? t(key(tool.id, `steps.${step.step}.desc`), { defaultValue: step.desc }) : "",
    install: (toolId: string, index: number, text: string) =>
      t(key(toolId, `install.${index}`), { defaultValue: text }),
  };
}
