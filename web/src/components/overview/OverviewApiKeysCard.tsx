import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { useApiKeySecrets } from "../../hooks/useApiKeySecrets";
import { getStoredApiKeySecret, maskApiKeySecret } from "../../lib/apiKeySecrets";
import { deleteApiKey, fetchApiKeyUsage, toggleApiKey } from "../apis/api";
import type { ApiKeyRecord, ApiKeyUsage } from "../apis/types";
import { formatLimitSummary } from "../apis/utils";
import { Button, ConfirmDialog, Toggle, cn } from "../ui";
import { ActionMenu, EmptyPanel, IconAction, SectionHead, ToolButton } from "../page-kit";
import { compactNumber, formatUsd } from "../upstreams/health";

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

export function OverviewApiKeysCard({ secret, apiKeys, onError, onMutated }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
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
      onError?.(error instanceof Error ? error.message : t("apis.failedLoadUsage"));
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
      onError?.(error instanceof Error ? error.message : t("apis.failedUpdateKey"));
    } finally {
      setTogglingId(null);
    }
  };

  const handleRevoke = (key: ApiKeyRecord) => {
    setConfirmState({
      title: t("apis.revokeKey"),
      message: t("overview.keys.revokeMessage", { name: key.name || key.id }),
      confirmText: t("overview.keys.revoke"),
      onConfirm: () => {
        void (async () => {
          try {
            await deleteApiKey(secret, key.id);
            onMutated?.();
            await loadUsage();
          } catch (error) {
            onError?.(error instanceof Error ? error.message : t("apis.failedRevokeKey"));
          }
        })();
      },
    });
  };

  // Pausing cuts clients off immediately, so it asks first; resuming does not.
  const requestToggle = (key: ApiKeyRecord, next: boolean) => {
    if (key.enabled && !next) {
      setConfirmState({
        title: t("overview.keys.pauseTitle"),
        message: t("overview.keys.pauseMessage", { name: key.name || key.id }),
        confirmText: t("overview.keys.pause"),
        onConfirm: () => void handleToggle(key, false),
      });
      return;
    }
    void handleToggle(key, next);
  };

  const enabledCount = apiKeys.filter((key) => key.enabled).length;
  const totals = apiKeys.reduce(
    (sum, key) => ({
      requests: sum.requests + (usageById[key.id]?.requests_today || 0),
      cost: sum.cost + (usageById[key.id]?.cost_usd_today || 0),
    }),
    { requests: 0, cost: 0 },
  );

  return (
    <section className="cp-panel ov-panel ov-keys" aria-label={t("overview.keys.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>vpn_key</span>}
        title={t("overview.keys.title")}
        subtitle={
          apiKeys.length
            ? t("overview.keys.summary", { count: apiKeys.length, on: enabledCount })
            : t("overview.keys.subtitle")
        }
        meta={
          apiKeys.length && !loadingUsage ? (
            <span className="ov-keys-today">{t("overview.keys.today", { requests: compactNumber(totals.requests), cost: formatUsd(totals.cost) })}</span>
          ) : null
        }
        actions={
          <ToolButton icon="open_in_new" onClick={() => navigate("/apis")} title={t("overview.keys.manageHint")}>
            {t("overview.keys.manage")}
          </ToolButton>
        }
      />

      {apiKeys.length === 0 ? (
        <EmptyPanel
          compact
          icon="vpn_key"
          title={t("overview.noApiKeys")}
          description={t("overview.keys.emptyHint")}
          action={<Button variant="primary" size="sm" icon="add" onClick={() => navigate("/apis")}>{t("overview.keys.create")}</Button>}
        />
      ) : (
        <div className="cp-list ov-keys-list">
          <div className="cp-list-head" aria-hidden>
            <span>{t("overview.keys.columns.key")}</span>
            <span>{t("overview.keys.columns.models")}</span>
            <span>{t("overview.keys.columns.limits")}</span>
            <span>{t("overview.keys.columns.today")}</span>
            <span>{t("overview.keys.columns.on")}</span>
            <span />
          </div>
          {apiKeys.map((key) => {
            const usage = usageById[key.id];
            const storedSecret = getStoredApiKeySecret(key.id);
            const models = key.models?.length ? key.models : null;
            const copyId = `secret_${key.id}`;
            return (
              <div key={key.id} className={cn("cp-list-row ov-key-row", key.enabled ? "is-on" : "is-off")}>
                <span className="cp-cell cp-cell-main ov-key-name">
                  <strong title={key.name || key.id}>{key.name || key.id}</strong>
                  {storedSecret ? (
                    <small className="ov-key-secret">
                      <code>{maskApiKeySecret(storedSecret)}</code>
                    </small>
                  ) : (
                    <small title={t("overview.keys.secretMissingHint")}>{t("overview.keys.secretMissing")}</small>
                  )}
                </span>
                <span className="cp-cell cp-cell-mono ov-key-models" data-label={t("overview.keys.columns.models")} title={models?.join("\n")}>
                  {models ? (models.length === 1 ? models[0] : t("overview.keys.modelsCount", { count: models.length })) : t("overview.keys.allModels")}
                </span>
                <span className="cp-cell cp-cell-mono ov-key-limits" data-label={t("overview.keys.columns.limits")} title={formatLimitSummary(key, t)}>
                  {formatLimitSummary(key, t)}
                </span>
                <span className="cp-cell cp-cell-mono ov-key-today" data-label={t("overview.keys.columns.today")}>
                  {loadingUsage && !usage
                    ? t("apis.loadingUsage")
                    : t("overview.keys.usage", { requests: compactNumber(usage?.requests_today || 0), cost: formatUsd(usage?.cost_usd_today || 0, 4) })}
                </span>
                <span className="cp-cell cp-keep-bright ov-key-toggle">
                  <Toggle
                    checked={key.enabled}
                    disabled={togglingId === key.id}
                    aria-label={key.enabled ? t("overview.keys.disable") : t("overview.keys.enable")}
                    title={key.enabled ? t("overview.keys.disable") : t("overview.keys.enable")}
                    onChange={(event) => requestToggle(key, event.target.checked)}
                  />
                </span>
                <span className="cp-cell cp-cell-actions cp-keep-bright">
                  {storedSecret ? (
                    <IconAction
                      icon={copied === copyId ? "check" : "content_copy"}
                      label={t("apis.copySecret")}
                      onClick={() => copy(storedSecret, copyId)}
                    />
                  ) : null}
                  <ActionMenu
                    label={t("overview.keys.more")}
                    items={[
                      {
                        key: "edit",
                        icon: "tune",
                        label: t("overview.keys.edit"),
                        hint: t("overview.keys.editHint"),
                        onSelect: () => navigate("/apis"),
                      },
                      {
                        key: "revoke",
                        icon: "block",
                        label: t("overview.keys.revoke"),
                        danger: true,
                        onSelect: () => handleRevoke(key),
                      },
                    ]}
                  />
                </span>
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
        confirmText={confirmState?.confirmText || t("common.confirm")}
        cancelText={t("common.cancel")}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />
    </section>
  );
}
