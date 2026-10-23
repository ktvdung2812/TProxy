import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { EmptyPanel } from "../page-kit";
import { RequestLogList, type RequestLogResolvers } from "./RequestLogList";
import { sortRequestLogs, type RequestSortKey as SortKey, type SortOrder as Order } from "./utils";

export type RequestSortKey = SortKey;
export type SortOrder = Order;

type Props = {
  logs: RequestLog[];
  sortKey: RequestSortKey;
  sortOrder: SortOrder;
  onSortKey: (key: RequestSortKey) => void;
  onSortOrder: (order: SortOrder) => void;
  resolvers?: RequestLogResolvers;
  onOpen?: (log: RequestLog) => void;
};

/**
 * Sortable request list for embedding (e.g. an account's request history);
 * the Logs page composes RequestLogList with its own toolbar instead.
 */
export function RequestLogsTable({ logs, sortKey, sortOrder, onSortKey, onSortOrder, resolvers, onOpen }: Props) {
  const { t } = useTranslation();
  const sorted = useMemo(() => sortRequestLogs(logs, sortKey, sortOrder), [logs, sortKey, sortOrder]);

  const handleSort = (key: RequestSortKey) => {
    if (key === sortKey) {
      onSortOrder(sortOrder === "asc" ? "desc" : "asc");
    } else {
      onSortKey(key);
      onSortOrder(key === "created_at" || key === "latency_ms" ? "desc" : "asc");
    }
  };

  return (
    <RequestLogList
      logs={sorted}
      resolvers={resolvers}
      onOpen={onOpen}
      sort={{ key: sortKey, order: sortOrder, onSort: handleSort }}
      empty={<EmptyPanel compact icon="receipt_long" title={t("logs.noRequestsLogged")} description={t("logs.requestsHint")} />}
    />
  );
}
