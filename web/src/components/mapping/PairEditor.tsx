import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../ui";
import { ActionMenu, EmptyPanel, IconAction, SectionHead, StatusPill, ToneCounts, type ActionMenuItem, type Tone } from "../page-kit";
import type { ModelOption } from "../../lib/modelOptions";
import { ModelTargetCombobox } from "./ModelTargetCombobox";
import type { PairSection, RowState } from "./useMappingEditor";

export type PairRow = {
  source: string;
  /** Friendly name (Cursor catalog) when it differs from the id. */
  label?: string;
  target: string;
  /** What the server routes the saved mapping to, when known. */
  routed?: string;
  state: RowState;
};

export const ROW_STATE_TONE: Record<RowState, Tone> = { saved: "success", new: "info", changed: "warning", incomplete: "danger" };

export type PairHandlers = {
  setTarget: (source: string, target: string) => void;
  remove: (source: string) => void;
  revert: (source: string) => void;
  select: (source: string, selected: boolean) => void;
  copy: (text: string, label: string) => void;
  /** Global models only: preview the rewrite chain for this source. */
  test?: (source: string) => void;
};

type Props = {
  section: PairSection;
  title: string;
  subtitle: ReactNode;
  icon: string;
  rows: PairRow[];
  /** Rows before search filtering, for counts and the empty state. */
  total: number;
  removedCount: number;
  selected: string[];
  onSelectAll: (select: boolean) => void;
  options: ModelOption[];
  handlers: PairHandlers;
  addRow: ReactNode;
  emptyTitle: string;
  emptyHint: string;
  hasSearch: boolean;
  onClearSearch: () => void;
  formatTarget: (target: string) => string;
};

export function PairEditor({
  section,
  title,
  subtitle,
  icon,
  rows,
  total,
  removedCount,
  selected,
  onSelectAll,
  options,
  handlers,
  addRow,
  emptyTitle,
  emptyHint,
  hasSearch,
  onClearSearch,
  formatTarget,
}: Props) {
  const { t } = useTranslation();
  const counts: Record<RowState, number> = { saved: 0, new: 0, changed: 0, incomplete: 0 };
  for (const row of rows) counts[row.state] += 1;
  const allSelected = rows.length > 0 && rows.every((row) => selected.includes(row.source));
  const someSelected = !allSelected && rows.some((row) => selected.includes(row.source));

  return (
    <section className="cp-panel mp-panel">
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>{icon}</span>}
        title={title}
        subtitle={subtitle}
        meta={
          <ToneCounts
            items={[
              { tone: "info", count: counts.new, label: t("mapping.status.new") },
              { tone: "warning", count: counts.changed + removedCount, label: t("mapping.status.changed") },
              { tone: "danger", count: counts.incomplete, label: t("mapping.status.incomplete") },
            ]}
          />
        }
      />
      <div className="mp-add">{addRow}</div>
      {rows.length > 0 ? (
        <div className="mp-rows-head">
          <input
            type="checkbox"
            className="cp-check"
            checked={allSelected}
            ref={(element) => {
              if (element) element.indeterminate = someSelected;
            }}
            onChange={(event) => onSelectAll(event.target.checked)}
            aria-label={t("mapping.selectAll")}
          />
          <span>{section === "cursor" ? t("mapping.columns.cursorModel") : t("mapping.columns.clientModel")}</span>
          <span>{t("mapping.columns.target")}</span>
        </div>
      ) : null}
      {rows.length === 0 ? (
        total > 0 && hasSearch ? (
          <EmptyPanel
            compact
            icon="filter_alt_off"
            title={t("mapping.noMatch")}
            description={t("mapping.noMatchHint")}
            action={<button type="button" className="cp-link-btn" onClick={onClearSearch}>{t("mapping.clearSearch")}</button>}
          />
        ) : (
          <EmptyPanel compact icon={icon} title={emptyTitle} description={emptyHint} />
        )
      ) : (
        <div className="mp-rows" role="list">
          {rows.map((row) => (
            <PairRowItem
              key={row.source}
              row={row}
              section={section}
              selected={selected.includes(row.source)}
              options={options}
              handlers={handlers}
              formatTarget={formatTarget}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function PairRowItem({
  row,
  section,
  selected,
  options,
  handlers,
  formatTarget,
}: {
  row: PairRow;
  section: PairSection;
  selected: boolean;
  options: ModelOption[];
  handlers: PairHandlers;
  formatTarget: (target: string) => string;
}) {
  const { t } = useTranslation();
  const name = row.label || row.source;
  const menu: ActionMenuItem[] = [
    (row.state === "changed" || row.state === "new" || row.state === "incomplete") && {
      key: "revert",
      icon: "undo",
      label: row.state === "new" ? t("mapping.action.discardRow") : t("mapping.action.revert"),
      hint: t("mapping.action.revertHint"),
      onSelect: () => handlers.revert(row.source),
    },
    { key: "copy-source", icon: "content_copy", label: t("mapping.action.copySource"), hint: row.source, onSelect: () => handlers.copy(row.source, row.source) },
    row.target.trim() && { key: "copy-target", icon: "content_copy", label: t("mapping.action.copyTarget"), hint: row.target, onSelect: () => handlers.copy(row.target, row.target) },
    handlers.test && { key: "test", icon: "play_arrow", label: t("mapping.action.test"), hint: t("mapping.action.testHint"), onSelect: () => handlers.test?.(row.source) },
    {
      key: "remove",
      icon: "delete",
      label: t("mapping.action.remove"),
      hint: t("mapping.action.removeHint"),
      danger: true,
      onSelect: () => handlers.remove(row.source),
    },
  ].filter(Boolean) as ActionMenuItem[];

  return (
    <div className={cn("mp-row mp-pair-row", `mp-state-${row.state}`, selected && "is-selected")} role="listitem">
      <input
        type="checkbox"
        className="cp-check mp-row-check"
        checked={selected}
        onChange={(event) => handlers.select(row.source, event.target.checked)}
        aria-label={t("mapping.selectRow", { name })}
      />
      <div className="mp-row-source">
        <div className="mp-row-title">
          <strong title={name}>{name}</strong>
          {row.state !== "saved" ? (
            <StatusPill tone={ROW_STATE_TONE[row.state]} title={t(`mapping.statusHint.${row.state}`)}>{t(`mapping.status.${row.state}`)}</StatusPill>
          ) : null}
        </div>
        {row.label && row.label !== row.source ? <code className="mp-row-id">{row.source}</code> : null}
      </div>
      <span className="mp-row-arrow material-symbols-outlined" aria-hidden>arrow_forward</span>
      <div className="mp-row-target">
        <ModelTargetCombobox
          aria-label={t("mapping.targetFor", { name: row.source })}
          value={row.target}
          placeholder={t("mapping.virtualModelOrProvider")}
          options={options}
          onChange={(next) => handlers.setTarget(row.source, next)}
        />
        {row.routed && row.state === "saved" ? (
          <small className="mp-row-routed" title={row.routed}>
            {t("mapping.routesNow")} <code>{formatTarget(row.routed)}</code>
          </small>
        ) : null}
      </div>
      <div className="mp-row-actions">
        {section === "models" && handlers.test ? (
          <IconAction icon="play_arrow" label={t("mapping.action.test")} onClick={() => handlers.test?.(row.source)} />
        ) : (
          <IconAction icon="content_copy" label={t("mapping.action.copySourceShort")} onClick={() => handlers.copy(row.source, row.source)} />
        )}
        <ActionMenu label={t("mapping.moreActions")} items={menu} />
      </div>
    </div>
  );
}
