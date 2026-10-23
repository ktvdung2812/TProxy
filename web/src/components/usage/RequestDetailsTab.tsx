import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, IconAction, PageNote, SectionHead, StatusPill, ToolButton } from "../page-kit";
import { buildCredentialIndex } from "../operations/utils";
import { ProviderLogo } from "../providers/ProviderLogo";
import type { Credential } from "../providers/types";
import { cn } from "../ui";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import { fetchUsageEvents, type UsageEvent } from "./api";
import { RequestDetailModal } from "./RequestDetailModal";
import { fmt, fmtCompact, fmtCostPrecise, fmtMs } from "./utils";

export type LogStatusFilter = "all" | "errors" | "ok";

type Props = {
  secret: string;
  providerNames: Record<string, string>;
  providerTypes: Record<string, string>;
  providerItems: Array<{ id: string; name: string; type: string }>;
  credentials: Record<string, Credential[]>;
  apiKeys: Array<{ id: string; name: string }>;
  providerFilter: string;
  statusFilter: LogStatusFilter;
  query: string;
  page: number;
  onPage: (page: number) => void;
  onTotal: (total: number) => void;
  reloadToken: number;
  onClearFilters: () => void;
  onError: (message: string) => void;
};

const PAGE_SIZE = 20;

export const isEventOk = (event: UsageEvent) => event.status >= 200 && event.status < 400;

function fmtStamp(iso: string) {
  const date = new Date(iso);
  return {
    time: date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }),
    day: date.toLocaleDateString([], { month: "short", day: "numeric" }),
  };
}

/**
 * Request log: one row per upstream attempt, newest first, paged on the
 * server (provider filter is server-side; status and search narrow the
 * loaded page). Rows open the full request detail.
 */
export function RequestDetailsTab({
  secret,
  providerNames,
  providerTypes,
  providerItems,
  credentials,
  apiKeys,
  providerFilter,
  statusFilter,
  query,
  page,
  onPage,
  onTotal,
  reloadToken,
  onClearFilters,
  onError,
}: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const [events, setEvents] = useState<UsageEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<UsageEvent | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await fetchUsageEvents(secret, {
        limit: PAGE_SIZE,
        offset: (page - 1) * PAGE_SIZE,
        providerId: providerFilter || undefined,
      });
      setEvents(result.data || []);
      setTotal(result.total || 0);
      onTotal(result.total || 0);
    } catch (error) {
      onError(error instanceof Error ? error.message : t("usage.log.failed"));
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }, [secret, page, providerFilter, onError, onTotal, reloadToken]);

  useEffect(() => {
    void load();
  }, [load]);

  const credentialIndex = useMemo(() => buildCredentialIndex(credentials, providerItems), [credentials, providerItems]);
  const keyNames = useMemo(() => new Map(apiKeys.map((key) => [key.id, key.name || key.id])), [apiKeys]);
  const accountLabel = useCallback(
    (credentialId: string) => maskAccountText(credentialIndex.get(credentialId)?.label || credentialId, masked),
    [credentialIndex, masked],
  );
  const keyLabel = useCallback((id?: string) => (id ? keyNames.get(id) || id : t("usage.log.localKey")), [keyNames, t]);

  const needle = query.trim().toLowerCase();
  const visible = events.filter((event) => {
    if (statusFilter === "errors" && isEventOk(event)) return false;
    if (statusFilter === "ok" && !isEventOk(event)) return false;
    if (!needle) return true;
    return [event.request_id, event.public_model_id, event.upstream_model, event.provider_id, providerNames[event.provider_id], keyLabel(event.client_api_key_id), event.error_code, accountLabel(event.credential_id)]
      .some((value) => (value || "").toLowerCase().includes(needle));
  });
  const pageErrors = events.filter((event) => !isEventOk(event)).length;
  const narrowed = statusFilter !== "all" || needle !== "";
  const hasFilters = narrowed || providerFilter !== "";

  const onRowKey = (event: KeyboardEvent<HTMLDivElement>, item: UsageEvent) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setSelected(item);
    }
  };

  return (
    <section className="us-section" aria-label={t("usage.toolbar.requestLog")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>receipt_long</span>}
        title={t("usage.toolbar.requestLog")}
        subtitle={t("usage.log.subtitle", { count: total, errors: pageErrors })}
        actions={
          <span className="us-pager">
            <ToolButton icon="chevron_left" disabled={page <= 1 || loading} onClick={() => onPage(page - 1)} aria-label={t("usage.previous")} />
            <span className="us-pager-label">{t("usage.pageOf", { page, total: totalPages })}</span>
            <ToolButton icon="chevron_right" disabled={page >= totalPages || loading} onClick={() => onPage(page + 1)} aria-label={t("usage.next")} />
          </span>
        }
      />
      {!loaded ? (
        <EmptyPanel loading title={t("common.loading")} />
      ) : visible.length === 0 ? (
        hasFilters ? (
          <EmptyPanel
            compact
            icon="filter_alt_off"
            title={narrowed ? t("usage.log.noMatchPage") : t("usage.noEventsFound")}
            description={narrowed && events.length > 0 ? t("usage.log.noMatchPageHint") : undefined}
            action={<button type="button" className="cp-link-btn" onClick={onClearFilters}>{t("usage.clearFilters")}</button>}
          />
        ) : (
          <EmptyPanel compact icon="receipt_long" title={t("usage.noEventsFound")} description={t("usage.log.emptyHint")} />
        )
      ) : (
        <div className={cn("cp-list us-log", loading && "is-refetching")}>
          <div className="cp-list-head" aria-hidden>
            <span>{t("usage.timestamp")}</span>
            <span>{t("usage.log.modelRoute")}</span>
            <span>{t("usage.apiKey")}</span>
            <span className="us-num">{t("usage.log.inOut")}</span>
            <span className="us-num">{t("usage.cached")}</span>
            <span className="us-num">{t("usage.latency")}</span>
            <span className="us-num">{t("usage.cost")}</span>
            <span>{t("usage.status")}</span>
            <span />
          </div>
          {visible.map((event) => {
            const ok = isEventOk(event);
            const stamp = fmtStamp(event.created_at);
            const providerName = providerNames[event.provider_id] || event.provider_id;
            return (
              <div
                key={`${event.request_id}-${event.created_at}-${event.attempt}`}
                role="button"
                tabIndex={0}
                className={cn("cp-list-row", !ok && "us-tone-danger")}
                onClick={() => setSelected(event)}
                onKeyDown={(keyEvent) => onRowKey(keyEvent, event)}
                aria-label={t("usage.log.openRow", { model: event.public_model_id, time: stamp.time })}
              >
                <span className="cp-cell cp-cell-main us-stamp" title={new Date(event.created_at).toLocaleString()}>
                  <strong>{stamp.time}</strong>
                  <small>{stamp.day}</small>
                </span>
                <span className="cp-cell us-entity">
                  <ProviderLogo className="us-logo" providerId={event.provider_id} providerType={providerTypes[event.provider_id] || event.provider_id} />
                  <span className="cp-cell-main">
                    <strong title={event.public_model_id || event.upstream_model}>{event.public_model_id || event.upstream_model}</strong>
                    <small title={`${providerName} · ${accountLabel(event.credential_id)}`}>{providerName} · {accountLabel(event.credential_id)}</small>
                  </span>
                </span>
                <span className="cp-cell cp-cell-mono us-ellipsis" title={event.client_api_key_id || ""}>{keyLabel(event.client_api_key_id)}</span>
                <span className="cp-cell us-num us-pair" title={`${fmt(event.input_tokens)} / ${fmt(event.output_tokens)}`}>
                  <span>{fmtCompact(event.input_tokens)}</span>
                  <small>{fmtCompact(event.output_tokens)}</small>
                </span>
                <span className={cn("cp-cell us-num", !event.cached_tokens && "us-dim")}>{event.cached_tokens ? fmtCompact(event.cached_tokens) : "—"}</span>
                <span className="cp-cell us-num us-pair">
                  <span>{fmtMs(event.latency_ms)}</span>
                  <small title={t("operations.firstToken")}>{event.ttft_ms == null ? "—" : t("usage.log.ttft", { value: fmtMs(event.ttft_ms) })}</small>
                </span>
                <span className={cn("cp-cell us-num", !event.estimated_cost_usd && "us-dim")}>{fmtCostPrecise(event.estimated_cost_usd)}</span>
                <span className="cp-cell us-status-cell">
                  <StatusPill tone={ok ? "success" : "danger"} title={event.error_code || undefined}>{event.status || t("usage.errorStatus")}</StatusPill>
                  {event.attempt > 1 ? <small className="us-dim" title={event.routing_reason}>{t("usage.log.attemptN", { count: event.attempt })}</small> : null}
                </span>
                <span className="cp-cell cp-cell-actions">
                  <IconAction
                    icon="open_in_full"
                    label={t("usage.requestDetails")}
                    onClick={(clickEvent) => {
                      clickEvent.stopPropagation();
                      setSelected(event);
                    }}
                  />
                </span>
              </div>
            );
          })}
        </div>
      )}
      {loaded && narrowed ? <PageNote icon="filter_alt">{t("usage.log.pageFilterNote")}</PageNote> : null}

      <RequestDetailModal
        event={selected}
        onClose={() => setSelected(null)}
        providerName={selected ? providerNames[selected.provider_id] || selected.provider_id : ""}
        providerType={selected ? providerTypes[selected.provider_id] || selected.provider_id : ""}
        accountLabel={selected ? accountLabel(selected.credential_id) : ""}
        keyLabel={selected ? keyLabel(selected.client_api_key_id) : ""}
      />
    </section>
  );
}
