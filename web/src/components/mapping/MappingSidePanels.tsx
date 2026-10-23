import { useTranslation } from "react-i18next";
import { Input } from "../ui";
import { EmptyPanel, SectionHead, StatusPill, ToolButton, type Tone } from "../page-kit";
import type { CursorMappingResponse } from "./api";

const CATALOG_TONE: Record<string, Tone> = { discovery: "success", mixed: "info", static: "warning" };

/** Where the Cursor model list came from, and a way to rediscover it. */
export function CursorCatalogPanel({
  data,
  catalogCount,
  unmappedCount,
  refreshing,
  disabled,
  onDiscover,
}: {
  data: CursorMappingResponse | null;
  catalogCount: number;
  unmappedCount: number;
  refreshing: boolean;
  disabled: boolean;
  onDiscover: () => void;
}) {
  const { t } = useTranslation();
  const source = data?.catalog_source || "";
  const sourceLabel =
    source === "discovery" ? t("mapping.liveDiscovery") : source === "mixed" ? t("mapping.discoveryStatic") : source ? t("mapping.staticFallback") : "—";
  return (
    <section className="cp-panel mp-panel mp-side">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>inventory_2</span>}
        title={t("mapping.catalog.title")}
        subtitle={t("mapping.catalog.subtitle")}
        actions={
          <ToolButton icon="sync" spinning={refreshing} disabled={refreshing || disabled} onClick={onDiscover}>
            {refreshing ? t("mapping.discovering") : t("mapping.discoverModels")}
          </ToolButton>
        }
      />
      <div className="mp-side-body">
        <dl className="cp-facts">
          <div>
            <dt>{t("mapping.catalog.models")}</dt>
            <dd>{catalogCount}</dd>
          </div>
          <div>
            <dt>{t("mapping.catalog.unmapped")}</dt>
            <dd>{unmappedCount}</dd>
          </div>
          <div>
            <dt>{t("mapping.catalog.source")}</dt>
            <dd>
              <StatusPill tone={CATALOG_TONE[source] || "neutral"}>{sourceLabel}</StatusPill>
            </dd>
          </div>
          <div>
            <dt>{t("mapping.catalog.provider")}</dt>
            <dd className="mp-mono">{data?.provider_id || "—"}</dd>
          </div>
        </dl>
        {data?.discovery_error ? (
          <p className="mp-note is-warning">
            <span className="material-symbols-outlined" aria-hidden>warning</span>
            <span>{data.discovery_error}</span>
          </p>
        ) : null}
        <p className="mp-note">
          <span className="material-symbols-outlined" aria-hidden>info</span>
          <span>{t("mapping.catalog.publicUrlNote")}</span>
        </p>
      </div>
    </section>
  );
}

/** Preview how the server rewrites a model name through the saved global map. */
export function ResolvePanel({
  input,
  onInput,
  result,
  running,
  onRun,
}: {
  input: string;
  onInput: (value: string) => void;
  result: string[];
  running: boolean;
  onRun: () => void;
}) {
  const { t } = useTranslation();
  return (
    <section className="cp-panel mp-panel mp-side">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>route</span>}
        title={t("mapping.resolve.title")}
        subtitle={t("mapping.resolve.subtitle")}
      />
      <div className="mp-side-body">
        <form
          className="mp-resolve-form"
          onSubmit={(event) => {
            event.preventDefault();
            onRun();
          }}
        >
          <Input
            value={input}
            onChange={(event) => onInput(event.target.value)}
            placeholder={t("mapping.resolve.placeholder")}
            aria-label={t("mapping.resolve.title")}
            autoComplete="off"
            spellCheck={false}
          />
          <ToolButton type="submit" icon="play_arrow" spinning={running} disabled={!input.trim() || running}>
            {t("mapping.resolve.run")}
          </ToolButton>
        </form>
        {result.length > 0 ? (
          <ol className="mp-chain" aria-label={t("mapping.resolve.chain")}>
            {result.map((step, index) => (
              <li key={`${step}-${index}`} className={index === result.length - 1 ? "is-final" : undefined}>
                <span className="mp-chain-index">{index === 0 ? t("mapping.resolve.requested") : index === result.length - 1 ? t("mapping.resolve.final") : `#${index}`}</span>
                <code>{step}</code>
              </li>
            ))}
          </ol>
        ) : (
          <p className="mp-note">
            <span className="material-symbols-outlined" aria-hidden>info</span>
            <span>{t("mapping.resolve.hint")}</span>
          </p>
        )}
      </div>
    </section>
  );
}

/** Read-only reference of the translation layers; folded away because it rarely changes. */
export function ProtocolReference({ mapping }: { mapping: Record<string, Record<string, string>> | undefined }) {
  const { t } = useTranslation();
  const entries = Object.entries(mapping || {});
  return (
    <details className="cp-panel mp-panel mp-reference">
      <summary>
        <span className="cp-section-icon material-symbols-outlined" aria-hidden>schema</span>
        <span className="mp-reference-title">
          <strong>{t("mapping.contentProtocolMapping")}</strong>
          <small>{t("mapping.contentProtocolDescShort", { count: entries.length })}</small>
        </span>
        <span className="material-symbols-outlined mp-reference-caret" aria-hidden>expand_more</span>
      </summary>
      {entries.length === 0 ? (
        <EmptyPanel compact icon="schema" title={t("mapping.noProtocolMapping")} />
      ) : (
        <div className="mp-protocols">
          {entries.map(([protocol, items]) => (
            <div className="mp-protocol" key={protocol}>
              <h4>{protocol}</h4>
              <dl>
                {Object.entries(items).map(([key, value]) => (
                  <div key={key}>
                    <dt>{key}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
        </div>
      )}
    </details>
  );
}
