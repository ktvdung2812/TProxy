import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CredentialQuota } from "../quota/api";
import { loadCredentialQuotaOnce } from "../quota/quotaOnce";
import { getColorTone } from "../quota/utils";
import type { Tone } from "../page-kit";
import type { NinerouterPreset } from "./api";
import { formatServicePlanLabel } from "./types";
import { maskAccountText } from "../../lib/accountMask";

/** Providers whose upstream quota/balance probe is implemented in tproxy. */
const CONNECTION_QUOTA_PROVIDER_IDS = new Set([
  "deepseek",
  "codex",
  "claude",
  "copilot",
  "github",
  "antigravity",
  "gemini-cli",
  "glm",
  "glm-cn",
  "minimax",
  "minimax-cn",
  "kiro",
  "qoder",
  "qwen",
  "xai",
  "grok-cli",
  "vercel-ai-gateway",
  "codebuddy-cn",
  "ollama",
  "kimi",
  "kimi-coding",
]);

export function providerSupportsUpstreamQuota(providerId: string, presets: NinerouterPreset[]) {
  if (CONNECTION_QUOTA_PROVIDER_IDS.has(providerId)) return true;
  return Boolean(presets.find((preset) => preset.id === providerId)?.supports_quota);
}

export type QuotaRow = {
  key: string;
  name: string;
  /** Remaining percentage; null for balances and unlimited windows. */
  percent: number | null;
  /** Display value: "18%", "∞" or an absolute balance such as "USD 0.12". */
  value: string;
  tone: Tone;
  resetAt?: string;
};

export type QuotaView = {
  rows: QuotaRow[];
  plan: string;
  message: string;
};

function quotaRows(quota: CredentialQuota | null | undefined): QuotaRow[] {
  if (!quota?.quotas) return [];
  return Object.entries(quota.quotas).map(([key, entry]) => {
    const name = (entry.name || key).trim();
    if (entry.unlimited) {
      return { key, name, percent: null, value: "∞", tone: "info" as const, resetAt: entry.reset_at };
    }
    // Absolute balance labels from DeepSeek-style probes: "USD 0.12"
    if (/^[A-Z]{3}\s+-?\d/.test(name)) {
      return { key, name, percent: null, value: name, tone: "info" as const, resetAt: entry.reset_at };
    }
    const total = entry.total || 0;
    const used = entry.used || 0;
    const remainingPct =
      typeof entry.remaining === "number" && entry.remaining > 0 && entry.remaining <= 100
        ? Math.round(entry.remaining)
        : total > 0
          ? Math.max(0, Math.round(((total - used) / total) * 100))
          : 100;
    return { key, name, percent: remainingPct, value: `${remainingPct}%`, tone: getColorTone(remainingPct), resetAt: entry.reset_at };
  });
}

type Options = {
  secret: string;
  providerId: string;
  supported: boolean;
  /** Credential ids in routing order — quota loads top to bottom. */
  orderedIds: string[];
  /** Changes whenever the set of credentials changes. */
  idsKey: string;
  masked: boolean;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
};

/**
 * Loads each account's upstream quota once, in routing order and one at a
 * time (the shared cache dedupes with the Quota Tracker), so the router-facing
 * controls only unlock after every account has answered.
 */
export function useAccountQuotas({ secret, providerId, supported, orderedIds, idsKey, masked, onError, onNotice }: Options) {
  const { t } = useTranslation();
  const [quotaById, setQuotaById] = useState<Record<string, QuotaView>>({});
  const [busyById, setBusyById] = useState<Record<string, boolean>>({});
  const [settledIds, setSettledIds] = useState<Set<string>>(() => new Set());
  const passRunningRef = useRef(false);
  const cancelledRef = useRef(false);
  const inFlightIdsRef = useRef(new Set<string>());
  const ensureLoadedRef = useRef<() => Promise<void>>(async () => {});
  const loadOrderRef = useRef(orderedIds);
  loadOrderRef.current = orderedIds;
  const settledRef = useRef(settledIds);
  settledRef.current = settledIds;

  // Forget accounts that were removed from the provider.
  useEffect(() => {
    const available = new Set(idsKey ? idsKey.split("\u0000") : []);
    const prune = <T,>(current: Record<string, T>) => {
      const next = Object.fromEntries(Object.entries(current).filter(([id]) => available.has(id)));
      return Object.keys(next).length === Object.keys(current).length ? current : next;
    };
    setQuotaById(prune);
    setBusyById(prune);
    setSettledIds((current) => {
      const next = new Set([...current].filter((id) => available.has(id)));
      return next.size === current.size ? current : next;
    });
  }, [idsKey]);

  const markSettled = useCallback((credentialId: string) => {
    settledRef.current = new Set(settledRef.current).add(credentialId);
    setSettledIds((current) => {
      if (current.has(credentialId)) return current;
      const next = new Set(current);
      next.add(credentialId);
      return next;
    });
  }, []);

  const loadQuota = useCallback(
    async (credentialId: string, silent = true, force = false) => {
      if (!supported) return;
      if (!force && (settledRef.current.has(credentialId) || inFlightIdsRef.current.has(credentialId))) return;
      inFlightIdsRef.current.add(credentialId);
      setBusyById((current) => ({ ...current, [credentialId]: true }));
      try {
        const cached = await loadCredentialQuotaOnce(secret, credentialId, force);
        if (cached.error) {
          setQuotaById((current) => ({ ...current, [credentialId]: { rows: [], plan: "", message: cached.error || t("providers.account.quotaFailed") } }));
          if (!silent) onError(cached.error);
          return;
        }
        const quota = cached.quota;
        if (!quota) return;
        const rows = quotaRows(quota);
        const message = quota.message || "";
        setQuotaById((current) => ({ ...current, [credentialId]: { rows, plan: formatServicePlanLabel(quota.plan), message } }));
        if (!silent && message && rows.length === 0) {
          onNotice(`${maskAccountText(credentialId, masked)}: ${message}`);
        }
      } catch (cause) {
        const message = cause instanceof Error ? cause.message : t("providers.account.quotaFailed");
        setQuotaById((current) => ({ ...current, [credentialId]: { rows: [], plan: "", message } }));
        if (!silent) onError(message);
      } finally {
        inFlightIdsRef.current.delete(credentialId);
        setBusyById((current) => ({ ...current, [credentialId]: false }));
        markSettled(credentialId);
      }
    },
    [markSettled, masked, onError, onNotice, secret, supported, t],
  );

  const ensureLoaded = useCallback(async () => {
    if (!supported || passRunningRef.current) return;
    if (loadOrderRef.current.length === 0) return;
    if (loadOrderRef.current.every((id) => settledRef.current.has(id))) return;
    passRunningRef.current = true;
    try {
      while (true) {
        if (cancelledRef.current) return;
        const nextId = loadOrderRef.current.find((id) => !settledRef.current.has(id) && !inFlightIdsRef.current.has(id));
        if (!nextId) break;
        await loadQuota(nextId, true, false);
      }
    } finally {
      passRunningRef.current = false;
    }
  }, [loadQuota, supported]);

  ensureLoadedRef.current = ensureLoaded;

  useEffect(() => {
    cancelledRef.current = false;
    void ensureLoadedRef.current();
    return () => {
      cancelledRef.current = true;
      passRunningRef.current = false;
    };
  }, [idsKey, supported, providerId]);

  const ready = !supported || orderedIds.every((id) => settledIds.has(id));
  const settledCount = orderedIds.filter((id) => settledIds.has(id)).length;

  return {
    quotaById,
    busyById,
    ready,
    settledCount,
    refresh: (credentialId: string) => loadQuota(credentialId, false, true),
  };
}
