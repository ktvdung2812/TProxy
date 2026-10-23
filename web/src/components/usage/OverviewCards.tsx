import type { PageStat } from "../page-kit";
import type { UsageStats } from "./api";
import { fmt, fmtCompact, fmtCost, isOkStatus } from "./utils";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Compact value with the exact number as tooltip, e.g. "16.4K" / "16,439". */
function compact(value: number) {
  return <span title={fmt(value)}>{fmtCompact(value)}</span>;
}

/**
 * Stat tiles for the selected period: traffic, tokens, cache, cost and the
 * failures among the live recent requests (the only error signal the stats
 * endpoint carries). The error tile doubles as the "errors only" filter.
 */
export function buildUsageStatItems({
  stats,
  t,
  recentErrors,
  errorsActive,
  onErrorsClick,
}: {
  stats: UsageStats;
  t: Translate;
  recentErrors: number;
  errorsActive: boolean;
  onErrorsClick: () => void;
}): PageStat[] {
  const inFlight = stats.activeRequests?.length ?? 0;
  const prompt = stats.totalPromptTokens || 0;
  const completion = stats.totalCompletionTokens || 0;
  const cached = stats.totalCachedTokens || 0;
  const cacheShare = prompt > 0 ? Math.round((cached / prompt) * 100) : 0;
  const recent = stats.recentRequests ?? [];
  const lastError = recent.find((item) => !isOkStatus(item.status));
  return [
    {
      key: "requests",
      icon: "swap_vert",
      label: t("usage.stats.requests"),
      value: compact(stats.totalRequests || 0),
      hint: inFlight ? t("usage.stats.inFlight", { count: inFlight }) : t("usage.stats.idle"),
      muted: !stats.totalRequests,
    },
    {
      key: "tokens",
      icon: "token",
      label: t("usage.stats.tokens"),
      value: compact(prompt + completion),
      hint: t("usage.stats.tokensHint", { input: fmtCompact(prompt), output: fmtCompact(completion) }),
      muted: prompt + completion === 0,
    },
    {
      key: "cache",
      icon: "cached",
      label: t("usage.stats.cache"),
      value: prompt > 0 ? `${cacheShare}%` : "—",
      hint: t("usage.stats.cacheHint", { read: fmtCompact(cached), write: fmtCompact(stats.totalCacheCreationTokens ?? 0) }),
      title: t("usage.stats.cacheTitle"),
      muted: cached === 0,
    },
    {
      key: "cost",
      icon: "payments",
      label: t("usage.stats.cost"),
      value: `~${fmtCost(stats.totalCost)}`,
      hint: t("usage.estCostHint"),
      mono: true,
      muted: !stats.totalCost,
    },
    {
      key: "errors",
      icon: "error",
      tone: recentErrors > 0 ? "danger" : "success",
      label: t("usage.stats.errors"),
      value: recentErrors,
      hint: recent.length
        ? lastError
          ? t("usage.stats.errorsHintLast", { count: recent.length, model: lastError.model })
          : t("usage.stats.errorsHint", { count: recent.length })
        : t("usage.stats.errorsNone"),
      muted: recentErrors === 0,
      active: errorsActive,
      onClick: onErrorsClick,
      title: t("usage.stats.errorsTitle"),
    },
  ];
}
