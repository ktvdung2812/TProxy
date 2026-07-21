import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Badge, Button, Card, Input, Select, Toggle } from "../ui";
import {
  GLOBAL_ROTATION_STRATEGIES,
  fetchAccountRotation,
  resetAccountRotationState,
  rotationStrategyLabel,
  saveAccountRotation,
  type AccountRotationSettings,
} from "../providers/api";
import {
  exportConfig,
  fetchAdminSettings,
  importConfig,
  reloadConfig,
  changeDashboardPassword,
  saveGatewaySettings,
  type AdminSettings,
} from "./api";

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
  onMutated?: () => void;
  onPasswordChanged?: (newPassword: string) => void;
};

const RETENTION_ROWS: Array<{ key: keyof AdminSettings["retention"]; label: string }> = [
  { key: "usage_events", label: "Usage events" },
  { key: "request_logs", label: "Request logs" },
  { key: "audit_events", label: "Audit events" },
  { key: "media_jobs", label: "Media jobs" },
  { key: "oauth_sessions", label: "OAuth sessions" },
  { key: "cleanup_interval", label: "Cleanup interval" },
];

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function SettingsView({ secret, onError, onNotice, onMutated, onPasswordChanged }: Props) {
  const importRef = useRef<HTMLInputElement>(null);
  const [loading, setLoading] = useState(true);
  const [settings, setSettings] = useState<AdminSettings | null>(null);
  const [rotation, setRotation] = useState<AccountRotationSettings | null>(null);
  const [strategy, setStrategy] = useState("round-robin");
  const [stickyLimit, setStickyLimit] = useState("3");
  const [savingRotation, setSavingRotation] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [exporting, setExporting] = useState<"json" | "yaml" | null>(null);
  const [importing, setImporting] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [savingPassword, setSavingPassword] = useState(false);
  const [allowLanManagement, setAllowLanManagement] = useState(false);
  const [publicBaseUrl, setPublicBaseUrl] = useState("");
  const [savingLanAccess, setSavingLanAccess] = useState(false);
  const [savingPublicUrl, setSavingPublicUrl] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [adminSettings, rotationSettings] = await Promise.all([
        fetchAdminSettings(secret),
        fetchAccountRotation(secret),
      ]);
      setSettings(adminSettings);
      setRotation(rotationSettings);
      setStrategy(rotationSettings.strategy || "round-robin");
      setStickyLimit(String(rotationSettings.sticky_round_robin_limit || 3));
      setAllowLanManagement(Boolean(adminSettings.allow_lan_management));
      setPublicBaseUrl(adminSettings.public_base_url || "");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load settings");
    } finally {
      setLoading(false);
    }
  }, [onError, secret]);

  useEffect(() => {
    void load();
  }, [load]);

  const saveRotation = async () => {
    if (!rotation) return;
    const sticky = Number(stickyLimit);
    if (!Number.isFinite(sticky) || sticky < 1) {
      onError("Sticky round-robin limit must be at least 1");
      return;
    }
    setSavingRotation(true);
    try {
      const saved = await saveAccountRotation(secret, {
        strategy,
        sticky_round_robin_limit: sticky,
        provider_strategies: rotation.provider_strategies,
      });
      setRotation(saved);
      onNotice?.("Account rotation settings saved");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to save rotation settings");
    } finally {
      setSavingRotation(false);
    }
  };

  const resetRotation = async () => {
    setSavingRotation(true);
    try {
      await resetAccountRotationState(secret);
      onNotice?.("Rotation runtime state reset");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to reset rotation state");
    } finally {
      setSavingRotation(false);
    }
  };

  const handleReload = async () => {
    setReloading(true);
    try {
      const result = await reloadConfig(secret);
      onNotice?.(result.config_path ? `Reloaded ${result.config_path}` : "Configuration reloaded");
      onMutated?.();
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to reload configuration");
    } finally {
      setReloading(false);
    }
  };

  const handleExport = async (format: "json" | "yaml") => {
    setExporting(format);
    try {
      const { blob, filename } = await exportConfig(secret, format);
      downloadBlob(blob, filename);
      onNotice?.(`Exported ${filename}`);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to export configuration");
    } finally {
      setExporting(null);
    }
  };

  const handleImportFile = async (file: File | undefined) => {
    if (!file) return;
    setImporting(true);
    try {
      await importConfig(secret, file);
      onNotice?.(`Imported ${file.name}`);
      onMutated?.();
      await load();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to import configuration");
    } finally {
      setImporting(false);
      if (importRef.current) importRef.current.value = "";
    }
  };

  const savePassword = async () => {
    if (newPassword !== confirmPassword) {
      onError("Mật khẩu mới không khớp");
      return;
    }
    if (newPassword.length < 6) {
      onError("Mật khẩu mới phải có ít nhất 6 ký tự");
      return;
    }
    setSavingPassword(true);
    try {
      await changeDashboardPassword(secret, currentPassword, newPassword);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      onPasswordChanged?.(newPassword);
      onNotice?.("Đã đổi mật khẩu dashboard");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Không đổi được mật khẩu");
    } finally {
      setSavingPassword(false);
    }
  };

  const toggleLanAccess = async (enabled: boolean) => {
    setSavingLanAccess(true);
    try {
      const result = await saveGatewaySettings(secret, { allow_lan_management: enabled });
      setAllowLanManagement(result.allow_lan_management);
      setSettings((current) => (current ? { ...current, allow_lan_management: result.allow_lan_management } : current));
      if (result.restart_required) {
        onNotice?.("Đã bật truy cập LAN. Đặt server.host thành 0.0.0.0 trong config.yaml và restart server.");
      } else {
        onNotice?.(enabled ? "Đã cho phép truy cập dashboard qua LAN" : "Đã tắt truy cập dashboard qua LAN");
      }
    } catch (error) {
      onError(error instanceof Error ? error.message : "Không cập nhật được cài đặt LAN");
    } finally {
      setSavingLanAccess(false);
    }
  };

  const savePublicBaseUrl = async () => {
    setSavingPublicUrl(true);
    try {
      const result = await saveGatewaySettings(secret, { public_base_url: publicBaseUrl.trim() });
      setPublicBaseUrl(result.public_base_url || "");
      setSettings((current) => (current ? { ...current, public_base_url: result.public_base_url || "" } : current));
      onNotice?.("Public base URL saved");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Không lưu được public base URL");
    } finally {
      setSavingPublicUrl(false);
    }
  };

  const overrideCount = Object.keys(rotation?.provider_strategies || {}).length;

  return (
    <section className="section settings-page">
      <Card pad="md" className="settings-card">
        <div className="settings-title-row">
          <span className="material-symbols-outlined">lock</span>
          <h2>Mật khẩu dashboard</h2>
        </div>
        <p className="settings-desc">
          Mật khẩu mặc định là <code>123123</code>. Đổi mật khẩu tại đây sau khi đăng nhập.
        </p>
        <div className="settings-form-grid">
          <label className="settings-field">
            <span>Mật khẩu hiện tại</span>
            <Input
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              disabled={savingPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </label>
          <label className="settings-field">
            <span>Mật khẩu mới</span>
            <Input
              type="password"
              autoComplete="new-password"
              value={newPassword}
              disabled={savingPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </label>
          <label className="settings-field">
            <span>Xác nhận mật khẩu mới</span>
            <Input
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              disabled={savingPassword}
              onChange={(event) => setConfirmPassword(event.target.value)}
            />
          </label>
        </div>
        <div className="settings-actions">
          <Button
            variant="primary"
            size="sm"
            disabled={savingPassword || !currentPassword || !newPassword || !confirmPassword}
            onClick={() => void savePassword()}
          >
            {savingPassword ? "Đang lưu…" : "Đổi mật khẩu"}
          </Button>
        </div>
      </Card>

      <Card pad="md" className="settings-card">
        <div className="settings-card-head">
          <div className="settings-title-row">
            <span className="material-symbols-outlined">sync_alt</span>
            <h2>Account rotation</h2>
            {overrideCount > 0 && (
              <Badge variant="neutral" size="sm">{overrideCount} provider override{overrideCount === 1 ? "" : "s"}</Badge>
            )}
          </div>
          <p className="settings-desc">
            Global fallback strategy when multiple credentials are available. Per-provider overrides can be configured on each provider detail page.
          </p>
        </div>
        <div className="settings-form-grid">
          <label className="settings-field">
            <span>Default strategy</span>
            <Select value={strategy} disabled={loading || savingRotation} onChange={(event) => setStrategy(event.target.value)}>
              {GLOBAL_ROTATION_STRATEGIES.map((item) => (
                <option key={item.id} value={item.id}>{item.label}</option>
              ))}
            </Select>
          </label>
          <label className="settings-field">
            <span>Sticky round-robin limit</span>
            <Input
              type="number"
              min={1}
              value={stickyLimit}
              disabled={loading || savingRotation}
              onChange={(event) => setStickyLimit(event.target.value)}
            />
          </label>
        </div>
        <p className="settings-hint">
          Active: {rotationStrategyLabel(strategy)} · sticky {stickyLimit} request{stickyLimit === "1" ? "" : "s"}
        </p>
        <div className="settings-actions">
          <Button variant="primary" size="sm" disabled={loading || savingRotation} onClick={() => void saveRotation()}>
            Save rotation
          </Button>
          <Button variant="outline" size="sm" disabled={loading || savingRotation} onClick={() => void resetRotation()}>
            Reset runtime state
          </Button>
        </div>
      </Card>

      <Card pad="md" className="settings-card">
        <div className="settings-title-row">
          <span className="material-symbols-outlined">schedule</span>
          <h2>Data retention</h2>
          <Badge variant="neutral" size="sm">config.yaml</Badge>
        </div>
        <p className="settings-desc">Read from the active server configuration. Edit config.yaml and reload to change these values.</p>
        <div className="settings-kv-table">
          {RETENTION_ROWS.map((row) => (
            <div key={row.key} className="settings-kv-row">
              <span>{row.label}</span>
              <code>{settings?.retention?.[row.key] || "—"}</code>
            </div>
          ))}
        </div>
      </Card>

      <Card pad="md" className="settings-card">
        <div className="settings-title-row">
          <span className="material-symbols-outlined">shield</span>
          <h2>Gateway</h2>
        </div>
        <div className="settings-kv-table">
          <div className="settings-kv-row">
            <span>Truy cập qua LAN</span>
            <div className="settings-inline">
              <Toggle
                checked={allowLanManagement}
                disabled={loading || savingLanAccess}
                onChange={(event) => void toggleLanAccess(event.target.checked)}
                aria-label="Cho phép truy cập dashboard qua LAN"
              />
              <Badge variant={allowLanManagement ? "success" : "neutral"} size="sm">
                {allowLanManagement ? "Đã bật" : "Tắt"}
              </Badge>
            </div>
          </div>
          {allowLanManagement ? (
            <p className="settings-hint">
              Thiết bị trong mạng LAN có thể mở dashboard bằng <code>http://&lt;ip-máy&gt;:{settings?.server_port || 28120}/dashboard/</code> và mật khẩu đăng nhập.
              {settings?.server_host === "127.0.0.1" || settings?.server_host === "localhost" || settings?.server_host === "::1" ? (
                <> Đặt <code>server.host: 0.0.0.0</code> trong config.yaml và restart server để LAN kết nối được.</>
              ) : null}
            </p>
          ) : null}
          <div className="settings-kv-row settings-kv-row-stack">
            <span>Public base URL</span>
            <div className="settings-inline settings-inline-stack">
              <Input
                value={publicBaseUrl}
                disabled={loading || savingPublicUrl}
                placeholder="https://your-tunnel.example.com"
                onChange={(event) => setPublicBaseUrl(event.target.value)}
              />
              <Button variant="outline" size="sm" loading={savingPublicUrl} onClick={() => void savePublicBaseUrl()}>
                Save
              </Button>
            </div>
          </div>
          <p className="settings-hint">
            Dùng cho CLI Tools như Cursor — endpoint phải truy cập được từ internet (tunnel, Tailscale Funnel, reverse proxy). Ví dụ: <code>https://abc.trycloudflare.com</code>
          </p>
          <div className="settings-kv-row">
            <span>Remote management</span>
            <Badge variant={settings?.allow_remote_management ? "success" : "neutral"} size="sm">
              {settings?.allow_remote_management ? "Allowed" : "Local only"}
            </Badge>
          </div>
          <div className="settings-kv-row">
            <span>Payload capture</span>
            <Badge variant={settings?.payload_capture ? "warning" : "neutral"} size="sm">
              {settings?.payload_capture ? "Enabled" : "Disabled"}
            </Badge>
          </div>
          <div className="settings-kv-row">
            <span>Token saver (RTK)</span>
            <div className="settings-inline">
              <Badge variant={settings?.token_saver?.rtk_enabled !== false ? "success" : "neutral"} size="sm">
                {settings?.token_saver?.rtk_enabled !== false ? "Enabled" : "Disabled"}
              </Badge>
              <Link className="settings-link" to="/token-saver">Configure</Link>
            </div>
          </div>
        </div>
      </Card>

      <Card pad="md" className="settings-card">
        <div className="settings-title-row">
          <span className="material-symbols-outlined">backup</span>
          <h2>Configuration</h2>
        </div>
        <p className="settings-desc">
          Export or import routing configuration. Reload reapplies the on-disk config file without restarting the process.
        </p>
        <div className="settings-actions">
          <Button
            variant="outline"
            size="sm"
            icon="download"
            disabled={exporting !== null || importing}
            onClick={() => void handleExport("json")}
          >
            {exporting === "json" ? "Exporting…" : "Export JSON"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            icon="download"
            disabled={exporting !== null || importing}
            onClick={() => void handleExport("yaml")}
          >
            {exporting === "yaml" ? "Exporting…" : "Export YAML"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            icon="upload"
            disabled={importing || exporting !== null}
            onClick={() => importRef.current?.click()}
          >
            {importing ? "Importing…" : "Import file"}
          </Button>
          <Button variant="primary" size="sm" icon="restart_alt" disabled={reloading} onClick={() => void handleReload()}>
            {reloading ? "Reloading…" : "Reload config"}
          </Button>
        </div>
        <input
          ref={importRef}
          type="file"
          accept=".json,.yaml,.yml,application/json,application/yaml"
          hidden
          onChange={(event) => void handleImportFile(event.target.files?.[0])}
        />
      </Card>
    </section>
  );
}
