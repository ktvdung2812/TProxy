import { useCallback, useEffect, useState } from "react";
import { Badge, Button, Card, Select } from "../ui";
import { fetchTokenSaverSettings, updateTokenSaverSettings, type CompressionMode } from "./api";

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
};

const COMPRESSION_MODES: Array<{ id: CompressionMode; label: string; hint: string }> = [
  { id: "ultra", label: "Ultra (RTK → CCR → Headroom → LLMLingua → Caveman)", hint: "Maximum savings; heuristic LLMLingua-2 pruning" },
  { id: "full", label: "Full (RTK → CCR → Headroom → Caveman)", hint: "All structural engines without LLMLingua" },
  { id: "stacked", label: "Stacked (RTK → Caveman)", hint: "Best savings for agentic sessions (~30–70%)" },
  { id: "rtk", label: "RTK only", hint: "Tool-output filtering for shell/git/build logs" },
  { id: "caveman", label: "Caveman", hint: "Terse prose compression on long assistant text" },
  { id: "lite", label: "Lite (Headroom)", hint: "Compact homogeneous JSON tool arrays" },
  { id: "off", label: "Off", hint: "Disable gateway compression" },
];

export function TokenSaverView({ secret, onError, onNotice }: Props) {
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [compressionMode, setCompressionMode] = useState<CompressionMode>("stacked");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchTokenSaverSettings(secret);
      setRtkEnabled(data.rtk_enabled !== false);
      const mode = (data.compression_mode || "stacked") as CompressionMode;
      setCompressionMode(COMPRESSION_MODES.some((item) => item.id === mode) ? mode : "stacked");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load token saver settings");
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const saveMode = async (nextMode: CompressionMode) => {
    setSaving(true);
    try {
      const nextEnabled = nextMode !== "off";
      await updateTokenSaverSettings(secret, { compression_mode: nextMode, rtk_enabled: nextEnabled });
      setCompressionMode(nextMode);
      setRtkEnabled(nextEnabled);
      onNotice?.(`Compression mode set to ${nextMode}`);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to update compression mode");
    } finally {
      setSaving(false);
    }
  };

  const copyInstall = async (command: string) => {
    try {
      await navigator.clipboard.writeText(command);
      onNotice?.("Copied to clipboard");
    } catch {
      onError("Could not copy command");
    }
  };

  const activeMode = COMPRESSION_MODES.find((item) => item.id === compressionMode) ?? COMPRESSION_MODES[0];

  return (
    <section className="section token-saver-page">
      <Card pad="md" className="token-saver-card">
        <div className="token-saver-card-head">
          <div>
            <div className="token-saver-title-row">
              <span className="material-symbols-outlined">compress</span>
              <h2>Compression pipeline</h2>
              <Badge variant={rtkEnabled ? "success" : "neutral"} size="sm">{rtkEnabled ? activeMode.label : "OFF"}</Badge>
            </div>
            <p className="token-saver-desc">
              Shrink prompts before they reach upstream providers. Stacked mode runs RTK on tool output, then Caveman on long prose.
            </p>
          </div>
        </div>
        <label className="settings-field">
          <span>Mode</span>
          <Select value={compressionMode} disabled={loading || saving} onChange={(event) => void saveMode(event.target.value as CompressionMode)}>
            {COMPRESSION_MODES.map((item) => (
              <option key={item.id} value={item.id}>{item.label}</option>
            ))}
          </Select>
        </label>
        <p className="token-saver-hint">{activeMode.hint}</p>
        <p className="token-saver-hint">
          Per-request override: <code>X-TProxy-Compression: stacked</code> · opt-out: <code>X-TProxy-Token-Saver: off</code>
        </p>
      </Card>

      <Card pad="md" className="token-saver-card">
        <div className="token-saver-title-row">
          <span className="material-symbols-outlined">terminal</span>
          <h2>CLI hook (optional)</h2>
        </div>
        <p className="token-saver-desc">
          For maximum savings, install the upstream RTK binary and enable the auto-rewrite hook in Claude Code / Cursor.
          tproxy compression runs on the gateway layer; the CLI hook compresses shell commands before tool results are created.
        </p>
        <div className="token-saver-command-block">
          <code>cargo install --git https://github.com/rtk-ai/rtk</code>
          <Button variant="outline" size="sm" onClick={() => void copyInstall("cargo install --git https://github.com/rtk-ai/rtk")}>
            Copy
          </Button>
        </div>
        <div className="token-saver-command-block">
          <code>rtk init -g</code>
          <Button variant="outline" size="sm" onClick={() => void copyInstall("rtk init -g")}>
            Copy
          </Button>
        </div>
      </Card>
    </section>
  );
}
