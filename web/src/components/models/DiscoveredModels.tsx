import { useTranslation } from "react-i18next";
import { ProviderLogo } from "../providers/ProviderLogo";
import { EmptyPanel, PageNote, SectionHead, Tag, ToolButton } from "../page-kit";
import type { ProviderOption } from "./types";
import { providerDisplayLabel, type DiscoveredPpmEntry } from "./utils";

type Props = {
  entries: DiscoveredPpmEntry[];
  totalCount: number;
  providers: ProviderOption[];
  credentialCounts: Record<string, number>;
  loading: boolean;
  ready: boolean;
  errors: Array<{ providerId: string; message: string }>;
  filtered: boolean;
  addingKey: string | null;
  disabled: boolean;
  onAdd: (entry: DiscoveredPpmEntry) => void;
  onRescan: () => void;
};

/**
 * Models that provider accounts expose but no public model routes to yet.
 * One click turns an entry into a public model with its first route.
 */
export function DiscoveredModels({
  entries,
  totalCount,
  providers,
  credentialCounts,
  loading,
  ready,
  errors,
  filtered,
  addingKey,
  disabled,
  onAdd,
  onRescan,
}: Props) {
  const { t } = useTranslation();
  const providerType = (id: string) => providers.find((provider) => provider.id === id)?.type || id;

  return (
    <section className="md-group" aria-label={t("models.discovered.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>travel_explore</span>}
        title={t("models.discovered.title")}
        subtitle={
          !ready
            ? t("models.discovered.scanning")
            : filtered && entries.length !== totalCount
              ? t("models.discovered.summaryFiltered", { shown: entries.length, count: totalCount })
              : t("models.discovered.summary", { count: totalCount })
        }
        actions={
          <ToolButton icon="sync" spinning={loading} disabled={loading} onClick={onRescan} title={t("models.discovered.rescanHint")}>
            {t("models.discovered.rescan")}
          </ToolButton>
        }
      />

      {errors.length > 0 ? (
        <div className="md-discovery-errors">
          {errors.map((error) => (
            <PageNote key={error.providerId} icon="error">
              <strong>{providerDisplayLabel(providers, error.providerId)}</strong>
              {" — "}
              {t("models.discovered.providerError", { message: error.message })}
            </PageNote>
          ))}
        </div>
      ) : null}

      {!ready ? (
        <div className="cp-grid md-discovered-grid">
          {[0, 1, 2].map((index) => (
            <div key={index} className="cp-card md-discovered-card">
              <div className="cp-skeleton"><span /><span /></div>
            </div>
          ))}
        </div>
      ) : entries.length === 0 ? (
        <EmptyPanel
          compact
          icon={filtered ? "filter_alt_off" : "task_alt"}
          title={filtered ? t("models.discovered.noMatch") : t("models.discovered.allConfigured")}
          description={filtered ? undefined : t("models.discovered.allConfiguredHint")}
        />
      ) : (
        <div className="cp-grid md-discovered-grid">
          {entries.map((entry) => {
            const adding = addingKey === entry.upstreamModel;
            const accounts = entry.providerIds.reduce((sum, id) => sum + (credentialCounts[id] ?? 0), 0);
            return (
              <article key={entry.upstreamModel} className="cp-card md-discovered-card">
                <header className="cp-card-head">
                  <div className="cp-card-title">
                    <strong title={entry.name}>{entry.name}</strong>
                    {entry.name !== entry.upstreamModel ? (
                      <code className="md-id" title={entry.upstreamModel}>{entry.upstreamModel}</code>
                    ) : null}
                  </div>
                  <ToolButton
                    icon="add"
                    spinning={adding}
                    disabled={disabled || adding}
                    onClick={() => onAdd(entry)}
                    title={t("models.discovered.addHint", { name: entry.name })}
                  >
                    {t("models.discovered.add")}
                  </ToolButton>
                </header>
                <div className="cp-card-body md-discovered-body">
                  <div className="md-discovered-providers">
                    {entry.providerIds.slice(0, 3).map((providerId) => (
                      <span key={providerId} className="md-provider-chip" title={t("models.accountCount", { count: credentialCounts[providerId] ?? 0 })}>
                        <ProviderLogo className="cp-tab-logo" providerId={providerId} providerType={providerType(providerId)} />
                        {providerDisplayLabel(providers, providerId)}
                      </span>
                    ))}
                    {entry.providerIds.length > 3 ? <span className="md-provider-chip">+{entry.providerIds.length - 3}</span> : null}
                  </div>
                  <div className="md-discovered-meta">
                    {entry.capabilities.slice(0, 3).map((capability) => (
                      <Tag key={capability} tone="neutral">{capability}</Tag>
                    ))}
                    <span className={accounts > 0 ? "" : "cp-tone-warning md-warn-text"}>
                      {t("models.accountCount", { count: accounts })}
                    </span>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
