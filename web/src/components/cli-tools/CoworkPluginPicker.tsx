import { useTranslation } from "react-i18next";
import type { CoworkPlugin } from "./api";
import { Toggle } from "../ui";

type Props = {
  catalog: CoworkPlugin[];
  /** null means "not chosen yet" — apply then falls back to the server defaults. */
  active: string[] | null;
  onChange: (next: string[]) => void;
};

/**
 * Managed MCP servers written into Claude Desktop's Cowork config. Only remote
 * HTTPS servers are offered: 9router bridges local stdio plugins through its own
 * MCP endpoint, which tproxy does not expose.
 */
export function CoworkPluginPicker({ catalog, active, onChange }: Props) {
  const { t } = useTranslation();
  if (catalog.length === 0) return null;

  const selected = active ?? catalog.map((plugin) => plugin.name);

  const toggle = (name: string, enabled: boolean) => {
    const next = enabled ? [...selected, name] : selected.filter((item) => item !== name);
    onChange(Array.from(new Set(next)));
  };

  return (
    <div className="cli-tool-field-stack">
      <p className="cli-tool-step-title">{t("cliTools.coworkPlugins")}</p>
      <p className="cli-tool-step-desc">{t("cliTools.coworkPluginsDesc")}</p>
      {catalog.map((plugin) => (
        <div key={plugin.name} className="cli-tool-plugin-row">
          <Toggle
            label={plugin.title || plugin.name}
            checked={selected.includes(plugin.name)}
            onChange={(event) => toggle(plugin.name, event.target.checked)}
          />
          <code>{plugin.url}</code>
        </div>
      ))}
    </div>
  );
}
