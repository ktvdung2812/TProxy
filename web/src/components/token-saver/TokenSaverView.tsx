import { useCallback, useEffect, useState } from "react";
import { Badge, Button, Card } from "../ui";
import { fetchTokenSaverSettings, updateTokenSaverSettings } from "./api";

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
};

export function TokenSaverView({ secret, onError, onNotice }: Props) {
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchTokenSaverSettings(secret);
      setRtkEnabled(data.rtk_enabled !== false);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load token saver settings");
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const toggleRTK = async (next: boolean) => {
    setSaving(true);
    try {
      await updateTokenSaverSettings(secret, { rtk_enabled: next });
      setRtkEnabled(next);
      onNotice?.(next ? "RTK token saver enabled" : "RTK token saver disabled");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to update RTK setting");
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

  return (
    <section className="section token-saver-page">
      <div className="page-head">
        <div>
          <p className="eyebrow">Optimization</p>
          <h1>Token Saver</h1>
          <p className="page-desc">
            Compress tool outputs before they reach the model. RTK integration is ported from the 9router pipeline and inspired by{" "}
            <a href="https://github.com/rtk-ai/rtk" target="_blank" rel="noreferrer">rtk-ai/rtk</a>.
          </p>
        </div>
      </div>

      <Card pad="md" className="token-saver-card">
        <div className="token-saver-card-head">
          <div>
            <div className="token-saver-title-row">
              <span className="material-symbols-outlined">compress</span>
              <h2>RTK Token Saver</h2>
              <Badge variant={rtkEnabled ? "success" : "neutral"} size="sm">{rtkEnabled ? "ON" : "OFF"}</Badge>
            </div>
            <p className="token-saver-desc">
              Smart filters for <code>git diff</code>, <code>grep</code>, <code>ls</code>, build logs, and other common tool outputs.
              Typical savings: 20–40% input tokens per agentic request.
            </p>
          </div>
          <label className="token-saver-toggle">
            <span>{rtkEnabled ? "Enabled" : "Disabled"}</span>
            <input
              type="checkbox"
              checked={rtkEnabled}
              disabled={loading || saving}
              onChange={(event) => void toggleRTK(event.target.checked)}
            />
          </label>
        </div>
        <p className="token-saver-hint">
          Per-request opt-out header: <code>X-TProxy-Token-Saver: off</code>
        </p>
      </Card>

      <Card pad="md" className="token-saver-card">
        <div className="token-saver-title-row">
          <span className="material-symbols-outlined">terminal</span>
          <h2>CLI hook (optional)</h2>
        </div>
        <p className="token-saver-desc">
          For maximum savings, install the upstream RTK binary and enable the auto-rewrite hook in Claude Code / Cursor.
          tproxy RTK runs on the proxy layer; the CLI hook compresses shell commands before tool results are created.
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
