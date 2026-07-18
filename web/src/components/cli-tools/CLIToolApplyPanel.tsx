import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { isAutoApplyTool, type CLITool } from "../../cli-tools/constants";
import { isLocalDashboardHost } from "../../devDefaults";
import { Badge, Button } from "../ui";
import {
  applyCLIToolConfig,
  fetchCLIToolStatus,
  resetCLIToolConfig,
  type CLIToolStatus,
} from "./api";
import { buildManualConfigs } from "./manualConfigs";
import { ManualConfigModal } from "./ManualConfigModal";
import { CLIApplyScriptBlock } from "./CLIApplyScriptBlock";

type Props = {
  tool: CLITool;
  secret: string;
  apiKey: string;
  model: string;
  baseUrl: string;
  onApiKeyChange: (value: string) => void;
};

function configBadge(status: CLIToolStatus | null): { label: string; tone: "success" | "warning" | "muted" } | null {
  if (!status?.installed) return null;
  if (status.has_tproxy || status.has_9router) {
    return { label: "Connected", tone: "success" };
  }
  return { label: "Not configured", tone: "warning" };
}

export function supportsAutoApply(toolId: string): boolean {
  return isAutoApplyTool(toolId);
}

export function CLIToolApplyPanel({ tool, secret, apiKey, model, baseUrl, onApiKeyChange }: Props) {
  const isLocal = isLocalDashboardHost();
  const canAutoApply = isLocal && supportsAutoApply(tool.id);
  const [status, setStatus] = useState<CLIToolStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [manualOpen, setManualOpen] = useState(false);

  const loadStatus = useCallback(async () => {
    if (!supportsAutoApply(tool.id)) return;
    setLoading(true);
    try {
      const next = await fetchCLIToolStatus(secret, tool.id);
      setStatus(next);
    } catch (error) {
      setStatus({ installed: false, has_tproxy: false, has_9router: false, message: String(error) });
    } finally {
      setLoading(false);
    }
  }, [secret, tool.id]);

  useEffect(() => {
    void loadStatus();
  }, [loadStatus]);

  const badge = configBadge(status);
  const manualConfigs = useMemo(
    () => buildManualConfigs(tool, baseUrl, apiKey, model),
    [tool, baseUrl, apiKey, model],
  );

  const handleApply = async () => {
    setApplying(true);
    setMessage(null);
    try {
      await applyCLIToolConfig(secret, tool.id, {
        baseUrl,
        apiKey: apiKey.trim(),
        model,
      });
      setMessage({ type: "success", text: "Configuration applied to local files." });
      await loadStatus();
    } catch (error) {
      setMessage({ type: "error", text: String(error) });
    } finally {
      setApplying(false);
    }
  };

  const handleReset = async () => {
    setResetting(true);
    setMessage(null);
    try {
      await resetCLIToolConfig(secret, tool.id);
      setMessage({ type: "success", text: "tproxy settings removed from local config." });
      await loadStatus();
    } catch (error) {
      setMessage({ type: "error", text: String(error) });
    } finally {
      setResetting(false);
    }
  };

  if (!supportsAutoApply(tool.id)) {
    return null;
  }

  return (
    <div className="cli-tool-apply-panel">
      <div className="cli-tool-apply-head">
        <div>
          <p className="cli-tool-apply-title">Configuration</p>
          <p className="cli-tool-apply-desc">
            {canAutoApply
              ? "Apply tproxy settings directly to config files on this machine."
              : "Auto-apply only works when the dashboard runs on localhost. Copy the config files below to your machine."}
          </p>
        </div>
        {badge ? (
          <Badge variant={badge.tone === "success" ? "success" : badge.tone === "warning" ? "warning" : "default"}>
            {badge.label}
          </Badge>
        ) : null}
      </div>

      {loading ? (
        <p className="cli-tool-hint">Checking local installation…</p>
      ) : status && !status.installed ? (
        <div className="cli-tool-note cli-tool-note-warning">
          <span className="material-symbols-outlined">warning</span>
          <p>
            {tool.name} not detected locally. You can still use{" "}
            <button type="button" className="cli-tool-inline-link" onClick={() => setManualOpen(true)}>
              manual configuration
            </button>{" "}
            if tproxy runs on another host.
          </p>
        </div>
      ) : null}

      {status?.config_path || status?.settings_path ? (
        <p className="cli-tool-hint">
          Config path: <code>{status.config_path || status.settings_path}</code>
        </p>
      ) : tool.settingsFile ? (
        <p className="cli-tool-hint">
          Config file: <code>{tool.settingsFile}</code>
        </p>
      ) : null}

      <div className="cli-tool-apply-actions">
        {canAutoApply ? (
          <>
            <Button variant="primary" size="sm" icon="check" disabled={applying || !model || !apiKey.trim()} onClick={() => void handleApply()}>
              {applying ? "Applying…" : "Apply to local config"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              icon="restart_alt"
              disabled={resetting || !(status?.has_tproxy || status?.has_9router)}
              onClick={() => void handleReset()}
            >
              {resetting ? "Resetting…" : "Reset"}
            </Button>
          </>
        ) : null}
        <Button variant="secondary" size="sm" icon="content_copy" onClick={() => setManualOpen(true)}>
          Manual config
        </Button>
      </div>

      <CLIApplyScriptBlock configs={manualConfigs} disabled={!model || !apiKey.trim()} />

      {!canAutoApply ? (
        <div className="cli-tool-note cli-tool-note-info">
          <span className="material-symbols-outlined">info</span>
          <p>
            Open <strong>Manual config</strong> to copy file contents, then paste them into the paths shown above on the
            machine where {tool.name} runs. Create API keys in <Link to="/apis">APIs</Link>.
          </p>
        </div>
      ) : null}

      {message ? (
        <div className={`cli-tool-note cli-tool-note-${message.type === "success" ? "info" : "error"}`}>
          <span className="material-symbols-outlined">{message.type === "success" ? "check_circle" : "error"}</span>
          <p>{message.text}</p>
        </div>
      ) : null}

      <ManualConfigModal
        open={manualOpen}
        onClose={() => setManualOpen(false)}
        title={`${tool.name} — manual configuration`}
        configs={manualConfigs}
      />
    </div>
  );
}
