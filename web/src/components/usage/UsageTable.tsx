import { Fragment, useEffect, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, SectionHead, Tag, ToolButton } from "../page-kit";
import { cn } from "../ui";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";
import type { UsageStats } from "./api";
import {
  enrichUsageRows,
  fmt,
  fmtCompact,
  fmtCost,
  fmtTime,
  groupUsageRows,
  sortUsageGroups,
  type UsageGroup,
  type UsageRow,
  type UsageSortField,
  type UsageTableView,
  type UsageValueMode,
} from "./utils";

type Sort = { field: UsageSortField; order: "asc" | "desc" };

type Props = {
  stats: UsageStats;
  tableView: UsageTableView;
  valueMode: UsageValueMode;
  sort: Sort;
  onSort: (sort: Sort) => void;
  query: string;
  providerId: string;
  providerLabel: string;
  onClearProvider: () => void;
  hasFilters: boolean;
  onClearFilters: () => void;
};

// Expanded groups persist per view under the keys the previous table used.
const STORAGE_KEYS: Record<UsageTableView, string> = {
  model: "tproxy-usage:expanded-models",
  account: "tproxy-usage:expanded-accounts",
  apiKey: "tproxy-usage:expanded-apikeys",
  endpoint: "tproxy-usage:expanded-endpoints",
};

const GROUP_ICONS: Record<UsageTableView, string> = { model: "neurology", account: "account_circle", apiKey: "key", endpoint: "dns" };

function readExpanded(key: string): Set<string> {
  try {
    const saved = window.localStorage.getItem(key);
    return new Set(saved ? (JSON.parse(saved) as string[]) : []);
  } catch {
    return new Set();
  }
}

const lower = (value?: string) => (value || "").toLowerCase();

function matchesProvider(row: UsageRow, id: string, label: string) {
  const provider = lower(row.provider);
  if (provider && (provider === lower(id) || provider === lower(label))) return true;
  // Bucket keys end in "(provider…)" or "|provider" when the name field is missing.
  return row.key.includes(`(${id}`) || row.key.endsWith(`|${id}`);
}

function ValueCells({ row, valueMode, grandTotal }: { row: UsageRow; valueMode: UsageValueMode; grandTotal: number }) {
  const tokens = valueMode === "tokens";
  const total = tokens ? row.totalTokens : row.totalCost || row.cost;
  const share = grandTotal > 0 ? total / grandTotal : 0;
  const cell = (value: number, empty = false) =>
    empty ? <span className="cp-cell us-num us-dim">—</span> : (
      <span className="cp-cell us-num" title={tokens ? fmt(value) : undefined}>{tokens ? fmtCompact(value) : fmtCost(value)}</span>
    );
  return (
    <>
      {cell(tokens ? row.promptTokens : row.inputCost)}
      {cell(tokens ? row.cachedTokens : row.cachedCost, !row.cachedTokens)}
      {cell(tokens ? row.completionTokens : row.outputCost)}
      <span className="cp-cell us-total" title={`${tokens ? fmt(total) : fmtCost(total)} · ${(share * 100).toFixed(1)}%`}>
        <strong>{tokens ? fmtCompact(total) : fmtCost(total)}</strong>
        <span className="us-share-track" aria-hidden>
          <span className="us-share-fill" style={{ width: `${share * 100}%` }} />
        </span>
      </span>
    </>
  );
}

/** Usage grouped by model, account, API key or upstream model; groups expand to their provider rows. */
export function UsageTable({
  stats,
  tableView,
  valueMode,
  sort,
  onSort,
  query,
  providerId,
  providerLabel,
  onClearProvider,
  hasFilters,
  onClearFilters,
}: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const storageKey = STORAGE_KEYS[tableView];
  const [expanded, setExpanded] = useState<Set<string>>(() => readExpanded(storageKey));

  useEffect(() => {
    setExpanded(readExpanded(storageKey));
  }, [storageKey]);

  const persist = (next: Set<string>) => {
    setExpanded(next);
    try {
      window.localStorage.setItem(storageKey, JSON.stringify([...next]));
    } catch {
      // Storage full or blocked: expansion just does not persist.
    }
  };

  const itemLabel = (item: UsageRow) => (tableView === "model" ? item.provider || "" : item.rawModel || item.key);

  const groups = useMemo(() => {
    const source =
      tableView === "model" ? stats.byModel : tableView === "account" ? stats.byAccount : tableView === "apiKey" ? stats.byApiKey : stats.byEndpoint;
    const needle = query.trim().toLowerCase();
    const rows = enrichUsageRows(source || {}).filter((row) => {
      if (providerId && !matchesProvider(row, providerId, providerLabel)) return false;
      if (!needle) return true;
      return [row.rawModel, row.provider, row.accountName, row.keyName, row.endpoint, row.key].some((value) => lower(value).includes(needle));
    });
    return sortUsageGroups(groupUsageRows(rows, tableView), sort.field, sort.order, (item) => (tableView === "model" ? item.provider || "" : item.rawModel || item.key));
  }, [stats, tableView, query, providerId, providerLabel, sort]);

  const totals = useMemo(() => {
    const sum = { requests: 0, promptTokens: 0, cachedTokens: 0, completionTokens: 0, totalTokens: 0, totalCost: 0, inputCost: 0, cachedCost: 0, outputCost: 0, cost: 0 };
    for (const group of groups) {
      for (const field of Object.keys(sum) as Array<keyof typeof sum>) sum[field] += (group.summary[field] as number) || 0;
    }
    return { ...sum, key: "__total" } as UsageRow;
  }, [groups]);
  const grandTotal = valueMode === "tokens" ? totals.totalTokens : totals.totalCost;
  const itemCount = groups.reduce((sum, group) => sum + group.items.length, 0);

  const toggle = (key: string) => {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    persist(next);
  };
  const allExpanded = groups.length > 0 && groups.every((group) => expanded.has(group.groupKey));

  const sortBy = (field: UsageSortField) => {
    if (sort.field === field) onSort({ field, order: sort.order === "asc" ? "desc" : "asc" });
    else onSort({ field, order: field === "name" ? "asc" : "desc" });
  };
  const totalField: UsageSortField = valueMode === "tokens" ? "tokens" : "cost";
  const header = (field: UsageSortField, label: string, numeric = false): ReactNode => {
    const active = sort.field === field;
    return (
      <button
        type="button"
        className={cn("us-sort", numeric && "us-num", active && "is-active")}
        onClick={() => sortBy(field)}
        aria-label={t("usage.table.sortBy", { column: label })}
      >
        <span>{label}</span>
        <span className="material-symbols-outlined" aria-hidden>
          {active ? (sort.order === "asc" ? "arrow_upward" : "arrow_downward") : "unfold_more"}
        </span>
      </button>
    );
  };

  const groupLabel = (group: UsageGroup) => {
    if (!group.groupKey) return t("usage.table.unknown");
    return tableView === "account" ? maskAccountText(group.groupKey, masked) : group.groupKey;
  };
  const groupSub = (group: UsageGroup) => {
    const providers = [...new Set(group.items.map((item) => item.provider).filter(Boolean))];
    if (tableView === "model") return t("usage.table.providersCount", { count: providers.length });
    const models = new Set(group.items.map((item) => item.rawModel).filter(Boolean)).size;
    return `${t("usage.table.modelsCount", { count: models })} · ${providers.join(", ")}`;
  };

  const onRowKey = (event: KeyboardEvent<HTMLDivElement>, key: string) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      toggle(key);
    }
  };

  const nameHeader = t(`usage.table.name_${tableView}`);

  return (
    <section className="us-section" aria-label={t(`usage.groupBy.${tableView}`)}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>{GROUP_ICONS[tableView]}</span>}
        title={t(`usage.groupBy.${tableView}`)}
        subtitle={t("usage.table.subtitle", { groups: groups.length, rows: itemCount, requests: fmt(totals.requests) })}
        meta={
          providerId ? (
            <button type="button" className="us-chip" onClick={onClearProvider} title={t("usage.providers.clearFilter")}>
              <Tag tone="primary">{t("usage.table.providerChip", { name: providerLabel })}</Tag>
              <span className="material-symbols-outlined" aria-hidden>close</span>
            </button>
          ) : null
        }
        actions={
          groups.length ? (
            <ToolButton
              icon={allExpanded ? "unfold_less" : "unfold_more"}
              onClick={() => persist(allExpanded ? new Set() : new Set(groups.map((group) => group.groupKey)))}
            >
              {allExpanded ? t("usage.table.collapseAll") : t("usage.table.expandAll")}
            </ToolButton>
          ) : null
        }
      />
      {groups.length === 0 ? (
        hasFilters ? (
          <EmptyPanel
            compact
            icon="filter_alt_off"
            title={t("usage.table.noMatch")}
            action={<button type="button" className="cp-link-btn" onClick={onClearFilters}>{t("usage.clearFilters")}</button>}
          />
        ) : (
          <EmptyPanel compact icon="hourglass_empty" title={t(`usage.table.empty_${tableView}`)} description={t("usage.table.emptyHint")} />
        )
      ) : (
        <div className="cp-list us-breakdown">
          <div className="cp-list-head">
            {header("name", nameHeader)}
            {header("requests", t("usage.requests"), true)}
            <span className="us-num">{t(valueMode === "tokens" ? "usage.table.inputTokens" : "usage.table.inputCost")}</span>
            <span className="us-num">{t(valueMode === "tokens" ? "usage.table.cachedTokens" : "usage.table.cachedCost")}</span>
            <span className="us-num">{t(valueMode === "tokens" ? "usage.table.outputTokens" : "usage.table.outputCost")}</span>
            {header(totalField, t(valueMode === "tokens" ? "usage.totalTokens" : "usage.totalCost"), true)}
            {header("lastUsed", t("usage.lastUsed"), true)}
          </div>
          {groups.map((group) => {
            const open = expanded.has(group.groupKey);
            return (
              <Fragment key={group.groupKey || "__unknown"}>
                <div
                  role="button"
                  tabIndex={0}
                  aria-expanded={open}
                  className={cn("cp-list-row us-group-row", open && "is-open")}
                  onClick={() => toggle(group.groupKey)}
                  onKeyDown={(event) => onRowKey(event, group.groupKey)}
                >
                  <span className="cp-cell us-group-name">
                    <span className="material-symbols-outlined us-chevron" aria-hidden>chevron_right</span>
                    <span className="cp-cell-main">
                      <strong className={cn(tableView === "endpoint" && "us-mono")} title={group.groupKey}>{groupLabel(group)}</strong>
                      <small>{groupSub(group)}</small>
                    </span>
                  </span>
                  <span className="cp-cell us-num">{fmt(group.summary.requests)}</span>
                  <ValueCells row={group.summary} valueMode={valueMode} grandTotal={grandTotal} />
                  <span className="cp-cell us-num us-dim">{fmtTime(group.summary.lastUsed, t)}</span>
                </div>
                {open
                  ? group.items.map((item) => (
                      <div key={item.key} className="cp-list-row us-item-row">
                        <span className="cp-cell us-item-name">
                          <span className="cp-cell-main">
                            <strong title={itemLabel(item)}>{itemLabel(item) || t("usage.table.unknown")}</strong>
                            {tableView !== "model" ? <small>{item.provider || "—"}</small> : null}
                          </span>
                        </span>
                        <span className="cp-cell us-num">{fmt(item.requests)}</span>
                        <ValueCells row={item} valueMode={valueMode} grandTotal={grandTotal} />
                        <span className="cp-cell us-num us-dim">{fmtTime(item.lastUsed, t)}</span>
                      </div>
                    ))
                  : null}
              </Fragment>
            );
          })}
          <div className="cp-list-row us-total-row">
            <span className="cp-cell us-group-name">
              <strong>{t("usage.table.total")}</strong>
            </span>
            <span className="cp-cell us-num">{fmt(totals.requests)}</span>
            <ValueCells row={totals} valueMode={valueMode} grandTotal={grandTotal} />
            <span className="cp-cell" />
          </div>
        </div>
      )}
    </section>
  );
}
