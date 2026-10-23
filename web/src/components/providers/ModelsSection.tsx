import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { EmptyPanel, IconAction, LinkButton, SearchField, SectionHead, StatusPill, Tag, ToneCounts, ToolButton } from "../page-kit";
import { cn } from "../ui";
import { discoverProviderModels, testModel } from "./api";
import type { DiscoveredModel } from "./api";
import type { Credential } from "./types";

type Props = {
  providerId: string;
  credentials: Credential[];
  secret: string;
  discoverNonce?: number;
  /** Re-runs discovery bypassing the cache; owned by the detail page so the notice is shared. */
  onDiscover?: () => void;
};

type TestResult = { ok: boolean; latency?: number; error?: string };

/** Upstream models discovered from the provider's enabled accounts, with per-model reachability tests. */
export function ModelsSection({ providerId, credentials, secret, discoverNonce = 0, onDiscover }: Props) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState<string | null>(null);
  const [discovering, setDiscovering] = useState(false);
  const [models, setModels] = useState<DiscoveredModel[]>([]);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Record<string, TestResult>>({});
  const [testingId, setTestingId] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);

  const enabledCount = useMemo(() => credentials.filter((credential) => credential.enabled).length, [credentials]);
  const normalized = query.trim().toLowerCase();

  const filtered = useMemo(() => {
    if (!normalized) return models;
    return models.filter((model) =>
      [model.id, model.name, model.owned_by, ...(model.capabilities || [])].filter(Boolean).join(" ").toLowerCase().includes(normalized),
    );
  }, [models, normalized]);

  useEffect(() => {
    if (enabledCount === 0) {
      setModels([]);
      setDiscoveryError(null);
      return;
    }
    let cancelled = false;
    setDiscovering(true);
    setDiscoveryError(null);
    discoverProviderModels(secret, providerId, discoverNonce > 0)
      .then((result) => {
        if (cancelled) return;
        setModels(result.data || []);
        if (result.error?.message) setDiscoveryError(result.error.message);
      })
      .catch((cause) => {
        if (cancelled) return;
        setModels([]);
        setDiscoveryError(cause instanceof Error ? cause.message : t("providers.modelDiscoveryFailed"));
      })
      .finally(() => {
        if (!cancelled) setDiscovering(false);
      });
    return () => {
      cancelled = true;
    };
    // t is left out on purpose: a language switch must not re-run discovery.
  }, [providerId, secret, enabledCount, discoverNonce]);

  const copy = async (id: string) => {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(id);
      window.setTimeout(() => setCopied(null), 1500);
    } catch {
      /* clipboard blocked */
    }
  };

  const runTest = async (model: DiscoveredModel) => {
    try {
      const result = await testModel(secret, { provider_id: providerId, model_id: model.id, kind: "llm", credential_ids: model.credential_ids });
      setResults((prev) => ({
        ...prev,
        [model.id]: { ok: result.ok, latency: result.latency_ms, error: result.ok ? undefined : result.error || t("providers.modelNotReachable") },
      }));
    } catch (cause) {
      setResults((prev) => ({ ...prev, [model.id]: { ok: false, error: cause instanceof Error ? cause.message : t("providers.testFailed") } }));
    }
  };

  const testOne = async (model: DiscoveredModel) => {
    if (testingId || testingAll) return;
    setTestingId(model.id);
    try {
      await runTest(model);
    } finally {
      setTestingId(null);
    }
  };

  const testAll = async () => {
    if (testingId || testingAll || models.length === 0) return;
    setTestingAll(true);
    try {
      for (const model of models) {
        setTestingId(model.id);
        await runTest(model);
      }
    } finally {
      setTestingId(null);
      setTestingAll(false);
    }
  };

  const tested = Object.values(results);
  const okCount = tested.filter((result) => result.ok).length;
  const failCount = tested.length - okCount;
  const subtitle = discovering
    ? t("providers.modelsSectionDiscoveringFrom")
    : enabledCount === 0
      ? t("providers.modelsSectionCardSubtitleConnect")
      : normalized
        ? t("providers.modelsSectionCardSubtitleFiltered", { filtered: filtered.length, total: models.length })
        : t("providers.models.summary", { count: models.length, accounts: enabledCount });

  return (
    <section className="cp-panel pv-panel pv-models" aria-label={t("providers.modelsTitle")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>apps</span>}
        title={t("providers.modelsTitle")}
        subtitle={subtitle}
        meta={
          tested.length > 0 ? (
            <ToneCounts
              label={t("providers.models.testResults")}
              items={[
                { tone: "success", count: okCount, label: t("providers.models.reachable") },
                { tone: "danger", count: failCount, label: t("providers.models.unreachable") },
              ]}
            />
          ) : null
        }
        actions={
          enabledCount > 0 ? (
            <>
              <ToolButton
                icon="science"
                spinning={testingAll}
                disabled={testingAll || testingId !== null || models.length === 0 || discovering}
                onClick={() => void testAll()}
                title={t("providers.testAllModels")}
              >
                {testingAll ? t("providers.testing") : t("providers.testAll")}
              </ToolButton>
              {onDiscover ? (
                <IconAction icon="refresh" label={t("providers.discoverModels")} spinning={discovering} disabled={discovering} onClick={onDiscover} />
              ) : null}
            </>
          ) : null
        }
      />

      {models.length > 0 ? (
        <div className="pv-models-tools">
          <SearchField value={query} onChange={setQuery} placeholder={t("providers.models.searchPlaceholder")} shortcut={false} className="pv-models-search" />
          {discoveryError ? <span className="pv-models-warning" title={discoveryError}>{discoveryError}</span> : null}
        </div>
      ) : null}

      {enabledCount === 0 ? (
        <EmptyPanel compact icon="key_off" title={t("providers.modelsSectionNoAccounts")} description={t("providers.modelsSectionNoAccountsHint")} />
      ) : discovering && models.length === 0 ? (
        <div className="cp-skeleton pv-models-skeleton" aria-label={t("providers.modelsSectionDiscovering")}>
          <span />
          <span />
          <span />
        </div>
      ) : models.length === 0 ? (
        <EmptyPanel
          compact
          icon={discoveryError ? "error" : "apps"}
          title={t("providers.modelsSectionNoModels")}
          description={discoveryError || t("providers.modelsSectionNoModelsHint")}
          action={onDiscover ? <LinkButton onClick={onDiscover}>{t("providers.discoverModels")}</LinkButton> : null}
        />
      ) : filtered.length === 0 ? (
        <EmptyPanel
          compact
          icon="search_off"
          title={t("providers.modelsSectionNoMatch", { query: query.trim() })}
          description={t("providers.modelsSectionNoMatchHint")}
          action={<LinkButton onClick={() => setQuery("")}>{t("providers.list.clearFilters")}</LinkButton>}
        />
      ) : (
        <div className="cp-list pv-model-list">
          <div className="cp-list-head" aria-hidden>
            <span>{t("providers.colModel")}</span>
            <span>{t("providers.colCapabilities")}</span>
            <span>{t("providers.models.colTest")}</span>
            <span />
          </div>
          {filtered.map((model) => {
            const result = results[model.id];
            const testing = testingId === model.id;
            const name = model.name?.trim();
            const accounts = model.credential_ids?.length ?? 0;
            return (
              <div key={model.id} className={cn("cp-list-row pv-model-row", result && (result.ok ? "is-ok" : "is-failed"))}>
                <span className="cp-cell cp-cell-main">
                  <strong title={name || model.id}>{name || model.id}</strong>
                  <small title={model.id}>
                    {name && name !== model.id ? model.id : model.owned_by || model.id}
                    {accounts > 0 ? ` · ${t("providers.models.accounts", { count: accounts })}` : ""}
                  </small>
                </span>
                <span className="cp-cell pv-model-caps">
                  {(model.capabilities || []).map((capability) => (
                    <Tag key={capability} tone="neutral">{capability}</Tag>
                  ))}
                </span>
                <span className="cp-cell">
                  {testing ? (
                    <StatusPill tone="info" loading>{t("providers.testing")}</StatusPill>
                  ) : result ? (
                    <StatusPill tone={result.ok ? "success" : "danger"} title={result.error}>
                      {result.ok
                        ? result.latency
                          ? t("providers.models.okLatency", { ms: result.latency })
                          : t("providers.models.ok")
                        : t("providers.models.failed")}
                    </StatusPill>
                  ) : (
                    <span className="cp-cell-mono pv-dim">—</span>
                  )}
                </span>
                <span className="cp-cell cp-cell-actions">
                  <IconAction
                    icon="science"
                    label={t("providers.testModel")}
                    spinning={testing}
                    disabled={testing || testingAll}
                    onClick={() => void testOne(model)}
                  />
                  <IconAction
                    icon={copied === model.id ? "check" : "content_copy"}
                    label={t("providers.copyModelId")}
                    active={copied === model.id}
                    onClick={() => void copy(model.id)}
                  />
                </span>
                {result && !result.ok && result.error ? (
                  <span className="pv-model-error" title={result.error}>{result.error}</span>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
