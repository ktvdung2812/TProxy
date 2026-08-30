import { useCallback, useEffect, useMemo, useState } from "react";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { checkCredentialHealth } from "../providers/api";
import { getProviderTypeInfo } from "../providers/catalog";
import { ProviderLogo } from "../providers/ProviderLogo";
import { RequestLogsTable, type RequestSortKey } from "../logs/RequestLogsTable";
import type { SortOrder } from "../logs/utils";
import { Badge, Button, Modal, Toggle, cn } from "../ui";
import { AccountTestChat } from "./AccountTestChat";
import { CredentialUsageChart } from "./CredentialUsageChart";
import {
  fetchCredentialQuota,
  fetchCredentialRequestLogs,
  type CredentialProxyUsage,
  type CredentialQuota,
} from "./api";
import { QuotaRingGrid } from "./QuotaRingGrid";
import { QuotaStackedBar } from "./QuotaStackedBar";
import { QuotaTable } from "./QuotaTable";
import {
  formatQuotaName,
  formatResetAbsolute,
  formatResetTime,
  getColorEmoji,
  getConnectionLabel,
  quotaEntries,
  usesQuotaRingLayout,
  usesQuotaStackedLayout,
} from "./utils";

type CredentialRow = {
  id: string;
  providerId: string;
  providerType: string;
  label?: string;
  email?: string;
  enabled: boolean;
  auth_type: string;
  created_at?: string;
};

type Props = {
  open: boolean;
  secret: string;
  credential: CredentialRow | null;
  quotaKey: string;
  quota?: CredentialQuota;
  proxyUsage?: CredentialProxyUsage;
  credentialActive: boolean;
  onClose: () => void;
  onToggleEnabled: (enabled: boolean) => void;
  toggling?: boolean;
  accountToggleReady?: boolean;
  onQuotaUpdated?: (quota: CredentialQuota) => void;
  refreshCountdown?: number;
};

function formatAuthType(value: string) {
  return value.replace(/_/g, " ");
}

function formatAddedAt(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function formatTokens(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}k`;
  return String(value);
}

export function QuotaAccountDetailModal({
  open,
  secret,
  credential,
  quotaKey,
  quota,
  proxyUsage,
  credentialActive,
  onClose,
  onToggleEnabled,
  toggling = false,
  accountToggleReady = true,
  onQuotaUpdated,
  refreshCountdown,
}: Props) {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState("");
  const [sortKey, setSortKey] = useState<RequestSortKey>("created_at");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");
  const [healthStatus, setHealthStatus] = useState<string>("");
  const [healthError, setHealthError] = useState("");
  const [healthLoading, setHealthLoading] = useState(false);
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [localQuota, setLocalQuota] = useState<CredentialQuota | undefined>(quota);

  useEffect(() => {
    setLocalQuota(quota);
  }, [quota, credential?.id]);

  const applyLogs = useCallback((items: RequestLog[]) => {
    if (!credential) return;
    setLogs(items.filter((item) => item.credential_id === credential.id));
  }, [credential]);

  useEffect(() => {
    if (!open || !credential) return;
    let cancelled = false;
    let controller: AbortController | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1000;

    setLogsLoading(true);
    setLogsError("");
    fetchCredentialRequestLogs(secret, credential.id)
      .then((response) => {
        if (!cancelled) applyLogs(response.data || []);
      })
      .catch((cause) => {
        if (!cancelled) {
          setLogsError(cause instanceof Error ? cause.message : "Failed to load request history");
        }
      })
      .finally(() => {
        if (!cancelled) setLogsLoading(false);
      });

    function scheduleReconnect() {
      if (cancelled || reconnectTimer !== null) return;
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        reconnectDelay = Math.min(reconnectDelay * 2, 30_000);
        void connectStream();
      }, reconnectDelay);
    }

    async function connectStream() {
      if (cancelled) return;
      controller?.abort();
      controller = new AbortController();
      const params = new URLSearchParams({ limit: "100", credential_id: credential!.id });
      try {
        // Management routes only accept a bearer credential, so the stream is
        // read through fetch; EventSource cannot set an Authorization header.
        const response = await fetch(`/api/admin/logs/stream?${params.toString()}`, {
          headers: secret ? { Authorization: `Bearer ${secret}` } : {},
          signal: controller.signal,
        });
        if (!response.ok || !response.body) throw new Error(`HTTP ${response.status}`);
        reconnectDelay = 1000;
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (!cancelled) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const frames = buffer.split(/\r?\n\r?\n/);
          buffer = frames.pop() || "";
          for (const frame of frames) {
            const dataLine = frame
              .split(/\r?\n/)
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart())
              .join("\n");
            if (!dataLine) continue;
            try {
              const payload = JSON.parse(dataLine) as { data?: RequestLog[] };
              applyLogs(payload.data || []);
            } catch {
              // Ignore malformed SSE payloads.
            }
          }
        }
      } catch {
        if (cancelled || controller.signal.aborted) return;
        scheduleReconnect();
        return;
      }
      if (!cancelled) scheduleReconnect();
    }

    void connectStream();

    return () => {
      cancelled = true;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      controller?.abort();
    };
  }, [open, secret, credential, applyLogs]);

  useEffect(() => {
    if (!open || !credential) return;
    let cancelled = false;
    setHealthLoading(true);
    setHealthError("");
    checkCredentialHealth(secret, credential.id)
      .then((result) => {
        if (!cancelled) {
          setHealthStatus(result.status || (result.ok ? "healthy" : "unhealthy"));
          setHealthError(result.last_error || result.error || "");
        }
      })
      .catch((cause) => {
        if (!cancelled) {
          setHealthError(cause instanceof Error ? cause.message : "Health check failed");
        }
      })
      .finally(() => {
        if (!cancelled) setHealthLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, secret, credential]);

  const refreshQuota = async () => {
    if (!credential) return;
    setQuotaLoading(true);
    try {
      const next = await fetchCredentialQuota(secret, credential.id);
      setLocalQuota(next);
      onQuotaUpdated?.(next);
    } catch (cause) {
      setHealthError(cause instanceof Error ? cause.message : "Failed to refresh quota");
    } finally {
      setQuotaLoading(false);
    }
  };

  const info = getProviderTypeInfo(quotaKey);
  const label = credential ? getConnectionLabel(credential) || credential.email || credential.id : "";
  const entries = useMemo(() => quotaEntries(localQuota), [localQuota]);
  const resetCountdowns = useMemo(
    () =>
      entries
        .filter((entry) => entry.reset_at)
        .map((entry) => ({
          key: entry.key,
          name: entry.name,
          remaining: entry.remaining,
          resetAt: entry.reset_at!,
          countdown: formatResetTime(entry.reset_at),
          absolute: formatResetAbsolute(entry.reset_at),
        }))
        .filter((entry) => entry.countdown !== "-")
        .sort((a, b) => new Date(a.resetAt).getTime() - new Date(b.resetAt).getTime()),
    [entries, refreshCountdown],
  );
  const usage = proxyUsage ?? { requests: 0, promptTokens: 0, completionTokens: 0 };
  const totalTokens = usage.promptTokens + usage.completionTokens;

  if (!credential) return null;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={info.name}
      subtitle={label}
      icon="account_circle"
      size="lg"
      className="quota-account-modal"
      footer={(
        <div className="quota-account-modal-footer">
          <Button variant="secondary" onClick={onClose}>Close</Button>
        </div>
      )}
    >
      <div className="quota-account-modal-layout">
        <div className="quota-account-modal-body">
        <div className="quota-account-modal-hero">
          <ProviderLogo className="quota-tracker-provider-icon quota-tracker-provider-icon-lg" providerType={quotaKey} style={{ color: info.color }} />
          <div className="quota-account-modal-hero-copy">
            <div className="quota-account-modal-title-row">
              <h4>{label}</h4>
              <Badge variant={credential.enabled ? "success" : "default"} size="sm" dot>
                {credential.enabled ? "Enabled" : "Disabled"}
              </Badge>
              {credentialActive ? (
                <Badge variant="info" size="sm" dot>Live</Badge>
              ) : null}
            </div>
            <p className="quota-account-modal-meta">{credential.email && credential.email !== label ? credential.email : credential.id}</p>
            <div className="quota-account-modal-toggle-row">
              <span>Account routing</span>
              <Toggle
                checked={credential.enabled}
                disabled={toggling || !accountToggleReady}
                onChange={(event) => onToggleEnabled(event.target.checked)}
                aria-label={credential.enabled ? "Disable account" : "Enable account"}
              />
            </div>
          </div>
        </div>

        <div className="quota-account-modal-stats">
          <div className="quota-account-modal-stat">
            <strong>{usage.requests}</strong>
            <span>Proxy requests</span>
          </div>
          <div className="quota-account-modal-stat">
            <strong>{formatTokens(totalTokens)}</strong>
            <span>Proxy tokens</span>
          </div>
          <div className="quota-account-modal-stat">
            <strong>{entries.length}</strong>
            <span>Quota windows</span>
          </div>
          <div className="quota-account-modal-stat">
            <strong>{logs.length}</strong>
            <span>Recent requests</span>
          </div>
        </div>

        <div className="quota-account-modal-section">
          <div className="quota-account-modal-section-head">
            <h5>Account details</h5>
          </div>
          <dl className="quota-account-modal-details">
            <div>
              <dt>Provider</dt>
              <dd>{info.name}</dd>
            </div>
            <div>
              <dt>Auth type</dt>
              <dd className="capitalize">{formatAuthType(credential.auth_type)}</dd>
            </div>
            <div>
              <dt>Credential ID</dt>
              <dd><code>{credential.id}</code></dd>
            </div>
            <div>
              <dt>Health</dt>
              <dd>
                {healthLoading ? "Checking…" : (
                  <Badge
                    variant={healthStatus === "healthy" ? "success" : healthStatus ? "warning" : "default"}
                    size="sm"
                  >
                    {healthStatus || "unknown"}
                  </Badge>
                )}
              </dd>
            </div>
            <div>
              <dt>Added</dt>
              <dd>{formatAddedAt(credential.created_at)}</dd>
            </div>
            {localQuota?.plan ? (
              <div>
                <dt>Plan</dt>
                <dd>{localQuota.plan}</dd>
              </div>
            ) : null}
            {localQuota?.renews_at ? (
              <div>
                <dt>Renews at</dt>
                <dd>{formatAddedAt(localQuota.renews_at)}</dd>
              </div>
            ) : null}
            {localQuota?.quota_auto_disabled ? (
              <div>
                <dt>Auto-paused</dt>
                <dd>Yes — quota reached 0%</dd>
              </div>
            ) : null}
          </dl>
          {healthError ? <p className="quota-account-modal-note quota-account-modal-note-error">{healthError}</p> : null}
          {localQuota?.message ? <p className="quota-account-modal-note">{localQuota.message}</p> : null}
        </div>

        <div className="quota-account-modal-section">
          <div className="quota-account-modal-section-head">
            <div className="quota-account-modal-section-head-main">
              <h5>Upstream quotas</h5>
              {resetCountdowns.length > 0 ? (
                <div className="quota-account-modal-section-countdowns">
                  {resetCountdowns.map((entry) => (
                    <span
                      key={entry.key}
                      className="quota-account-modal-section-countdown"
                      title={entry.absolute ? `${formatQuotaName(entry.name)} · ${entry.absolute}` : formatQuotaName(entry.name)}
                    >
                      <span aria-hidden>{getColorEmoji(entry.remaining)}</span>
                      <span>{formatQuotaName(entry.name)} in {entry.countdown}</span>
                    </span>
                  ))}
                </div>
              ) : null}
            </div>
            <button
              type="button"
              className="quota-tracker-icon-btn"
              disabled={quotaLoading}
              onClick={() => void refreshQuota()}
              aria-label="Refresh quota"
              title="Refresh quota"
            >
              <span className={cn("material-symbols-outlined", quotaLoading && "animate-spin")}>refresh</span>
            </button>
          </div>
          {entries.length > 0 ? (
            usesQuotaRingLayout(quotaKey) ? (
              <QuotaRingGrid rows={entries} proxyUsage={usage} />
            ) : usesQuotaStackedLayout(quotaKey) ? (
              <QuotaStackedBar rows={entries} proxyUsage={usage} />
            ) : (
              <QuotaTable
                rows={entries}
                credentialActive={credentialActive}
                proxyUsage={usage}
              />
            )
          ) : (
            <p className="quota-account-modal-empty">No quota data loaded for this account.</p>
          )}
        </div>

        <CredentialUsageChart secret={secret} credentialId={credential.id} active={open} />

        <div className="quota-account-modal-section">
          <div className="quota-account-modal-section-head">
            <h5>Request history</h5>
            <span className="quota-account-modal-section-meta">Recent proxy traffic routed through this account</span>
          </div>
          {logsLoading && logs.length === 0 ? (
            <div className="quota-account-modal-state">
              <span className="material-symbols-outlined animate-spin">progress_activity</span>
              <span>Loading request history…</span>
            </div>
          ) : logsError ? (
            <div className="quota-account-modal-state quota-account-modal-note-error">{logsError}</div>
          ) : (
            <RequestLogsTable
              logs={logs}
              sortKey={sortKey}
              sortOrder={sortOrder}
              onSortKey={setSortKey}
              onSortOrder={setSortOrder}
            />
          )}
        </div>
        </div>

        <AccountTestChat
          secret={secret}
          credentialId={credential.id}
          credentialEnabled={credential.enabled}
        />
      </div>
    </Modal>
  );
}
