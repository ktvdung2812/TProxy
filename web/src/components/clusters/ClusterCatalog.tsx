import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, cn } from "../ui";
import { EmptyPanel, LinkButton, PageNote, SearchField, SectionHead, Tag, Toolbar, ToolbarEnd, ToolbarRow } from "../page-kit";
import { SaveBar } from "./ClusterPolicies";
import type { ClusterCatalog, ModelPrice, SharedCatalog, SnapshotApiKey, SnapshotCombo, SnapshotModel } from "./types";
import { toAmount, useDraft } from "./utils";

type ShareKind = "models" | "combos" | "api_keys";
type ShareRow = { id: string; label: string; enabled: boolean; missing: boolean };

const EMPTY_SHARED: SharedCatalog = { models: [], combos: [], api_keys: [], prices: {} };
const EMPTY_PRICE: ModelPrice = { input_per_million: 0, output_per_million: 0, reasoning_per_million: 0, request: 0 };

function hasPrice(price?: ModelPrice) {
  return Boolean(price && (price.input_per_million || price.output_per_million || price.reasoning_per_million || price.request));
}

/** Local items first, then shared ids that no longer exist here (so they can be unshared). */
function shareRows(local: Array<{ id: string; label: string; enabled: boolean }>, sharedIds: string[]): ShareRow[] {
  const known = new Set(local.map((item) => item.id));
  return [
    ...local.map((item) => ({ ...item, missing: false })),
    ...[...new Set(sharedIds)].filter((id) => !known.has(id)).map((id) => ({ id, label: id, enabled: true, missing: true })),
  ];
}

function matches(row: { id: string; label: string }, query: string) {
  return !query || row.id.toLowerCase().includes(query) || row.label.toLowerCase().includes(query);
}

function RowTags({ row }: { row: ShareRow }) {
  const { t } = useTranslation();
  return (
    <>
      {row.missing ? <Tag tone="warning" title={t("clusters.catalog.missingHint")}>{t("clusters.catalog.missing")}</Tag> : null}
      {!row.enabled ? <Tag tone="neutral" title={t("clusters.catalog.offHint")}>{t("clusters.catalog.off")}</Tag> : null}
    </>
  );
}

function SelectLinks({ onAll, onNone, disabled }: { onAll: () => void; onNone: () => void; disabled?: boolean }) {
  const { t } = useTranslation();
  return (
    <span className="cl-head-links">
      <LinkButton onClick={onAll} disabled={disabled}>{t("clusters.catalog.all")}</LinkButton>
      <LinkButton onClick={onNone} disabled={disabled}>{t("clusters.catalog.none")}</LinkButton>
    </span>
  );
}

function ShareChecklist({
  icon,
  title,
  rows,
  selected,
  query,
  note,
  emptyText,
  disabled,
  onToggle,
}: {
  icon: string;
  title: string;
  rows: ShareRow[];
  selected: string[];
  query: string;
  note?: ReactNode;
  emptyText: string;
  disabled?: boolean;
  onToggle: (ids: string[], on: boolean) => void;
}) {
  const { t } = useTranslation();
  const on = new Set(selected);
  const visible = rows.filter((row) => matches(row, query));
  return (
    <section className="cp-panel cl-panel">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>{icon}</span>}
        title={title}
        subtitle={t("clusters.catalog.sharedCount", { shared: selected.length, total: rows.length })}
        actions={
          rows.length ? (
            <SelectLinks disabled={disabled} onAll={() => onToggle(visible.map((row) => row.id), true)} onNone={() => onToggle(visible.map((row) => row.id), false)} />
          ) : null
        }
      />
      {note ? <PageNote icon="info">{note}</PageNote> : null}
      {rows.length === 0 ? (
        <p className="cl-muted">{emptyText}</p>
      ) : visible.length === 0 ? (
        <p className="cl-muted">{t("clusters.catalog.noMatch")}</p>
      ) : (
        <ul className="cl-checklist">
          {visible.map((row) => (
            <li key={row.id}>
              <label className={cn("cl-check-row", !on.has(row.id) && "is-off")}>
                <input
                  type="checkbox"
                  className="cp-check"
                  checked={on.has(row.id)}
                  disabled={disabled}
                  onChange={(event) => onToggle([row.id], event.target.checked)}
                />
                <span className="cl-check-copy">
                  <strong title={row.label}>{row.label}</strong>
                  {row.label !== row.id ? <small title={row.id}>{row.id}</small> : null}
                </span>
                <RowTags row={row} />
              </label>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/* ------------------------------------------------------------
   Hub: choose what members receive
   ------------------------------------------------------------ */
export function SharedCatalogEditor({
  shared,
  models,
  combos,
  apiKeys,
  busy,
  onSave,
}: {
  shared: SharedCatalog | null;
  models: SnapshotModel[];
  combos: SnapshotCombo[];
  apiKeys: SnapshotApiKey[];
  busy: string | null;
  onSave: (next: SharedCatalog) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const server = shared ?? EMPTY_SHARED;
  const { draft, setDraft, dirty, reset, markSaved } = useDraft(server);
  const [search, setSearch] = useState("");
  const query = search.trim().toLowerCase();
  const saving = busy === "shared";

  const modelRows = useMemo(
    () => shareRows(models.map((model) => ({ id: model.ID, label: model.DisplayName || model.ID, enabled: model.Enabled !== false })), [...server.models, ...draft.models]),
    [models, server.models, draft.models],
  );
  const comboRows = useMemo(
    () => shareRows(combos.map((combo) => ({ id: combo.id, label: combo.display_name || combo.id, enabled: combo.enabled !== false })), [...server.combos, ...draft.combos]),
    [combos, server.combos, draft.combos],
  );
  const keyRows = useMemo(
    () => shareRows(apiKeys.map((key) => ({ id: key.id, label: key.name || key.id, enabled: key.enabled !== false })), [...server.api_keys, ...draft.api_keys]),
    [apiKeys, server.api_keys, draft.api_keys],
  );

  const setShared = (kind: ShareKind, rows: ShareRow[], ids: string[], on: boolean) =>
    setDraft((current) => {
      const next = new Set(current[kind]);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      const ordered = rows.map((row) => row.id).filter((id) => next.has(id));
      return { ...current, [kind]: ordered };
    });

  const setPrice = (id: string, field: "input_per_million" | "output_per_million", value: string) =>
    setDraft((current) => {
      const price = { ...(current.prices[id] ?? EMPTY_PRICE), [field]: toAmount(value) };
      const prices = { ...current.prices };
      if (hasPrice(price)) prices[id] = price;
      else delete prices[id];
      return { ...current, prices };
    });

  const save = async () => {
    const selected = new Set(draft.models);
    const payload: SharedCatalog = {
      models: draft.models,
      combos: draft.combos,
      api_keys: draft.api_keys,
      // Prices only matter for models that are actually shared.
      prices: Object.fromEntries(Object.entries(draft.prices).filter(([id, price]) => selected.has(id) && hasPrice(price))),
    };
    if (await onSave(payload)) markSaved();
  };

  const sharedModels = new Set(draft.models);
  const visibleModels = modelRows.filter((row) => matches(row, query));
  const allVisibleOn = visibleModels.length > 0 && visibleModels.every((row) => sharedModels.has(row.id));
  const someVisibleOn = visibleModels.some((row) => sharedModels.has(row.id));
  const totalShared = draft.models.length + draft.combos.length + draft.api_keys.length;

  return (
    <div className="cl-catalog">
      <Toolbar>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("clusters.catalog.search")} />
          <ToolbarEnd>
            <SaveBar dirty={dirty} saving={saving} onSave={() => void save()} onReset={reset} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>
      <PageNote icon="share">{t("clusters.catalog.hubNote", { count: totalShared })}</PageNote>

      <section className="cl-section" aria-labelledby="cl-share-models-title">
        <SectionHead
          leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>hub</span>}
          title={<span id="cl-share-models-title">{t("clusters.catalog.models")}</span>}
          subtitle={t("clusters.catalog.sharedCount", { shared: draft.models.length, total: modelRows.length })}
          actions={
            modelRows.length ? (
              <SelectLinks
                disabled={saving}
                onAll={() => setShared("models", modelRows, visibleModels.map((row) => row.id), true)}
                onNone={() => setShared("models", modelRows, visibleModels.map((row) => row.id), false)}
              />
            ) : null
          }
        />
        {modelRows.length === 0 ? (
          <EmptyPanel compact icon="hub" title={t("clusters.catalog.noModels")} description={t("clusters.catalog.noModelsHint")} />
        ) : visibleModels.length === 0 ? (
          <p className="cl-muted">{t("clusters.catalog.noMatch")}</p>
        ) : (
          <div className="cp-list cl-share-models" role="table" aria-labelledby="cl-share-models-title">
            <div className="cp-list-head" role="row">
              <span role="columnheader">
                <input
                  type="checkbox"
                  className="cp-check"
                  checked={allVisibleOn}
                  disabled={saving}
                  ref={(element) => {
                    if (element) element.indeterminate = someVisibleOn && !allVisibleOn;
                  }}
                  onChange={(event) => setShared("models", modelRows, visibleModels.map((row) => row.id), event.target.checked)}
                  aria-label={t("clusters.catalog.toggleAllModels")}
                />
              </span>
              <span role="columnheader">{t("clusters.catalog.columns.model")}</span>
              <span role="columnheader" title={t("clusters.catalog.priceHint")}>{t("clusters.catalog.columns.input")}</span>
              <span role="columnheader" title={t("clusters.catalog.priceHint")}>{t("clusters.catalog.columns.output")}</span>
            </div>
            {visibleModels.map((row, index) => {
              const on = sharedModels.has(row.id);
              const price = draft.prices[row.id];
              const checkId = `cl-share-model-${index}`;
              return (
                <div key={row.id} role="row" className={cn("cp-list-row cl-share-row", !on && "is-off")}>
                  <span role="cell" className="cp-cell cp-keep-bright">
                    <input
                      id={checkId}
                      type="checkbox"
                      className="cp-check"
                      checked={on}
                      disabled={saving}
                      onChange={(event) => setShared("models", modelRows, [row.id], event.target.checked)}
                    />
                  </span>
                  <label role="cell" htmlFor={checkId} className="cp-cell cp-cell-main cl-share-name">
                    <span className="cl-node-name">
                      <strong title={row.label}>{row.label}</strong>
                      <RowTags row={row} />
                    </span>
                    {row.label !== row.id ? <small title={row.id}>{row.id}</small> : null}
                  </label>
                  {(["input_per_million", "output_per_million"] as const).map((field) => (
                    <span key={field} role="cell" className="cp-cell cp-keep-bright">
                      <Input
                        type="number"
                        min={0}
                        step={0.01}
                        inputMode="decimal"
                        className="cl-price-input"
                        disabled={!on || saving}
                        value={price?.[field] ? price[field] : ""}
                        placeholder="—"
                        onChange={(event) => setPrice(row.id, field, event.target.value)}
                        aria-label={t(field === "input_per_million" ? "clusters.catalog.inputPriceFor" : "clusters.catalog.outputPriceFor", { model: row.id })}
                      />
                    </span>
                  ))}
                </div>
              );
            })}
          </div>
        )}
        <PageNote icon="payments">{t("clusters.catalog.priceNote")}</PageNote>
      </section>

      <div className="cl-share-grid">
        <ShareChecklist
          icon="layers"
          title={t("clusters.catalog.combos")}
          rows={comboRows}
          selected={draft.combos}
          query={query}
          disabled={saving}
          emptyText={t("clusters.catalog.noCombos")}
          onToggle={(ids, on) => setShared("combos", comboRows, ids, on)}
        />
        <ShareChecklist
          icon="vpn_key"
          title={t("clusters.catalog.apiKeys")}
          rows={keyRows}
          selected={draft.api_keys}
          query={query}
          disabled={saving}
          note={t("clusters.catalog.apiKeysNote")}
          emptyText={t("clusters.catalog.noApiKeys")}
          onToggle={(ids, on) => setShared("api_keys", keyRows, ids, on)}
        />
      </div>
    </div>
  );
}

/* ------------------------------------------------------------
   Member: what arrived from the hub (read-only) and conflicts
   ------------------------------------------------------------ */
function ReadOnlyList({
  icon,
  title,
  rows,
  conflicted,
  query,
  emptyText,
}: {
  icon: string;
  title: string;
  rows: Array<{ id: string; label: string }>;
  conflicted: Set<string>;
  query: string;
  emptyText: string;
}) {
  const { t } = useTranslation();
  const visible = rows.filter((row) => matches(row, query));
  return (
    <section className="cp-panel cl-panel">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>{icon}</span>}
        title={title}
        subtitle={t("clusters.catalog.receivedCount", { count: rows.length })}
      />
      {rows.length === 0 ? (
        <p className="cl-muted">{emptyText}</p>
      ) : visible.length === 0 ? (
        <p className="cl-muted">{t("clusters.catalog.noMatch")}</p>
      ) : (
        <ul className="cl-checklist">
          {visible.map((row) => (
            <li key={row.id} className="cl-check-row is-static">
              <span className="material-symbols-outlined cl-lock" aria-hidden>lock</span>
              <span className="cl-check-copy">
                <strong title={row.label}>{row.label}</strong>
                {row.label !== row.id ? <small title={row.id}>{row.id}</small> : null}
              </span>
              {conflicted.has(row.id) ? (
                <Tag tone="warning" title={t("clusters.catalog.localWinsHint")}>{t("clusters.catalog.localWins")}</Tag>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

const EMPTY_CATALOG: ClusterCatalog = { version: "", models: [], combos: [], api_keys: [], conflicts: [] };

export function SyncedCatalog({ catalog, busy, onSync }: { catalog: ClusterCatalog | null; busy: string | null; onSync: () => void }) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const query = search.trim().toLowerCase();
  const data = catalog ?? EMPTY_CATALOG;
  const conflictIds = (kinds: string[]) => new Set(data.conflicts.filter((conflict) => kinds.includes(conflict.kind)).map((conflict) => conflict.id));

  return (
    <div className="cl-catalog">
      <Toolbar>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("clusters.catalog.search")} />
          <ToolbarEnd>
            {data.version ? <span className="cl-muted cl-version" title={data.version}>{t("clusters.catalog.version", { version: data.version })}</span> : null}
            <Button size="sm" variant="secondary" icon="sync" loading={busy === "sync"} disabled={busy !== null} onClick={onSync}>
              {t("clusters.syncNow")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>
      <PageNote icon="lock">{t("clusters.catalog.memberNote")}</PageNote>

      {data.conflicts.length ? (
        <section className="cp-panel cl-panel cl-conflicts" aria-labelledby="cl-conflicts-title">
          <SectionHead
            leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>warning</span>}
            title={<span id="cl-conflicts-title">{t("clusters.catalog.conflictsTitle", { count: data.conflicts.length })}</span>}
            subtitle={t("clusters.catalog.conflictsHint")}
          />
          <ul className="cl-conflict-list">
            {data.conflicts.map((conflict, index) => (
              <li key={`${conflict.kind}:${conflict.id}:${index}`}>
                <Tag tone="warning">{t(`clusters.catalog.kind.${conflict.kind}`, { defaultValue: conflict.kind || "—" })}</Tag>
                <code title={conflict.id}>{conflict.id || "—"}</code>
                <span>{conflict.reason || t("clusters.catalog.conflictDefault")}</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <div className="cl-synced-grid">
        <ReadOnlyList
          icon="hub"
          title={t("clusters.catalog.models")}
          rows={data.models.map((item) => ({ id: item.id, label: item.display_name || item.id }))}
          conflicted={conflictIds(["model", "alias"])}
          query={query}
          emptyText={t("clusters.catalog.noneReceived")}
        />
        <ReadOnlyList
          icon="layers"
          title={t("clusters.catalog.combos")}
          rows={data.combos.map((item) => ({ id: item.id, label: item.display_name || item.id }))}
          conflicted={conflictIds(["combo"])}
          query={query}
          emptyText={t("clusters.catalog.noneReceived")}
        />
        <ReadOnlyList
          icon="vpn_key"
          title={t("clusters.catalog.apiKeys")}
          rows={data.api_keys.map((item) => ({ id: item.id, label: item.name || item.id }))}
          conflicted={conflictIds(["api_key"])}
          query={query}
          emptyText={t("clusters.catalog.noneReceived")}
        />
      </div>
    </div>
  );
}
