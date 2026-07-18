import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { useApiKeySecrets } from "../../hooks/useApiKeySecrets";
import { getStoredApiKeySecret, maskApiKeySecret } from "../../lib/apiKeySecrets";
import { deleteApiKey, fetchApiKeyUsage, toggleApiKey } from "../apis/api";
import type { ApiKeyRecord, ApiKeyUsage } from "../apis/types";
import { formatLimitSummary } from "../apis/utils";
import { Card, ConfirmDialog, EmptyState, Toggle, cn } from "../ui";

type Props = {
  secret: string;
  apiKeys: ApiKeyRecord[];
  onError?: (message: string) => void;
  onMutated?: () => void;
};

type ConfirmState = {
  title: string;
  message: string;
  confirmText: string;
  onConfirm: () => void;
};

function usd(value: number) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 4,
  }).format(value || 0);
}

function compact(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
}

export function OverviewApiKeysCard({ secret, apiKeys, onError, onMutated }: Props) {
  useApiKeySecrets();
  const { copied, copy } = useCopyToClipboard();
  const [usageById, setUsageById] = useState<Record<string, ApiKeyUsage>>({});
  const [loadingUsage, setLoadingUsage] = useState(true);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [confirmState, setConfirmState] = useState<ConfirmState | null>(null);

  const loadUsage = useCallback(async () => {
    setLoadingUsage(true);
    try {
      const result = await fetchApiKeyUsage(secret);
      const mapped: Record<string, ApiKeyUsage> = {};
      for (const item of result.api_keys || []) {
        mapped[item.id] = item;
      }
      setUsageById(mapped);
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to load API key usage");
    } finally {
      setLoadingUsage(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void loadUsage();
  }, [loadUsage, apiKeys]);

  const handleToggle = async (key: ApiKeyRecord, enabled: boolean) => {
    setTogglingId(key.id);
    try {
      await toggleApiKey(secret, key, enabled);
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to update API key");
    } finally {
      setTogglingId(null);
    }
  };

  const handleRevoke = (key: ApiKeyRecord) => {
    setConfirmState({
      title: "Revoke API key",
      message: `Revoke "${key.name || key.id}"? Clients using this key will lose access immediately.`,
      confirmText: "Revoke",
      onConfirm: () => {
        void (async () => {
          try {
            await deleteApiKey(secret, key.id);
            onMutated?.();
            await loadUsage();
          } catch (error) {
            onError?.(error instanceof Error ? error.message : "Failed to revoke API key");
          }
        })();
      },
    });
  };

  return (
    <Card className="overview-api-keys-card" pad="md">
      <div className="overview-api-keys-head">
        <div>
          <h3 className="overview-api-keys-title">
            <span className="material-symbols-outlined">vpn_key</span>
            API keys
          </h3>
          <p className="overview-api-keys-desc">Client keys for OpenAI-compatible access.</p>
        </div>
        <div className="overview-api-keys-actions">
          <span className="meta">{apiKeys.length} keys</span>
          <Link to="/apis" className={cn("btn", "btn-outline", "btn-sm")}>
            <span className="material-symbols-outlined">open_in_new</span>
            Manage
          </Link>
        </div>
      </div>

      {apiKeys.length === 0 ? (
        <EmptyState icon="api" text="No API keys yet." hint="Create a key to authenticate clients." />
      ) : (
        <div className="overview-api-keys-list">
          {apiKeys.map((key) => {
            const usage = usageById[key.id];
            const requests = usage?.requests_today || 0;
            const spent = usage?.cost_usd_today || 0;
            const models = key.models?.length ? key.models.join(", ") : "*";
            const storedSecret = getStoredApiKeySecret(key.id);
            const usageText = loadingUsage
              ? "Loading usage…"
              : `${compact(requests)} requests today · ${usd(spent)}`;

            return (
              <div key={key.id} className={key.enabled ? "overview-api-key-row" : "overview-api-key-row is-paused"}>
                <div className="overview-api-key-main">
                  <p className="overview-api-key-name">{key.name || key.id}</p>
                  <div className="overview-api-key-meta-row">
                    <p className="overview-api-key-meta">
                      <span className="overview-api-key-id">
                        {storedSecret ? (
                          <>
                            <code>{maskApiKeySecret(storedSecret)}</code>
                            <button
                              type="button"
                              className="endpoint-row-copy small"
                              onClick={() => copy(storedSecret, `secret_${key.id}`)}
                              aria-label="Copy API key"
                            >
                              <span className="material-symbols-outlined">
                                {copied === `secret_${key.id}` ? "check" : "content_copy"}
                              </span>
                            </button>
                          </>
                        ) : (
                          <span className="apis-key-secret-missing">Secret not saved</span>
                        )}
                      </span>
                      <span className="overview-api-key-sep">·</span>
                      <span>{models}</span>
                      <span className="overview-api-key-sep">·</span>
                      <span>{formatLimitSummary(key)}</span>
                      <span className="overview-api-key-sep">·</span>
                      <span className="overview-api-key-usage">{usageText}</span>
                    </p>
                    <div className="overview-api-key-actions">
                      <Toggle
                        label=""
                        checked={key.enabled}
                        disabled={togglingId === key.id}
                        aria-label={key.enabled ? "Disable API key" : "Enable API key"}
                        onChange={(event) => {
                          const next = event.target.checked;
                          if (key.enabled && !next) {
                            setConfirmState({
                              title: "Pause API key",
                              message: `Pause "${key.name || key.id}"? Clients using this key will stop working immediately.`,
                              confirmText: "Pause",
                              onConfirm: () => void handleToggle(key, false),
                            });
                            return;
                          }
                          void handleToggle(key, next);
                        }}
                      />
                      <button
                        type="button"
                        className="overview-api-key-revoke"
                        onClick={() => handleRevoke(key)}
                        aria-label={`Revoke ${key.name || key.id}`}
                      >
                        <span className="material-symbols-outlined">block</span>
                        Revoke
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <ConfirmDialog
        open={confirmState !== null}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        variant="danger"
        confirmText={confirmState?.confirmText || "Confirm"}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />
    </Card>
  );
}
