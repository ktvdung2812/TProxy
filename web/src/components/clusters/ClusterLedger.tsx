import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../ui";
import { EmptyPanel, SectionHead, Segmented, Tag } from "../page-kit";
import type { LedgerRow } from "./types";
import { aggregateLedger, fmtInt, fmtUsd, ledgerTotals } from "./utils";

export type LedgerDays = "1" | "7";

type Props = {
  available: boolean;
  rows: LedgerRow[];
  days: LedgerDays;
  onDaysChange: (days: LedgerDays) => void;
  /** This node, so its own lines can be marked. */
  selfId: string;
  loaded: boolean;
  error: string;
};

function NodeRef({ name, id, self }: { name: string; id: string; self: boolean }) {
  const { t } = useTranslation();
  return (
    <span className="cl-ledger-node" title={id}>
      <span>{name || id || "—"}</span>
      {self ? <Tag tone="primary">{t("clusters.nodes.thisNode")}</Tag> : null}
    </span>
  );
}

/** Who consumed how much from whom: one line per lender → borrower pair, summed over the window. */
export function ClusterLedger({ available, rows, days, onDaysChange, selfId, loaded, error }: Props) {
  const { t, i18n } = useTranslation();
  const pairs = useMemo(() => aggregateLedger(rows), [rows]);
  const totals = useMemo(() => ledgerTotals(pairs), [pairs]);
  const language = i18n.language;

  return (
    <section className="cl-section" aria-labelledby="cl-ledger-title">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>receipt_long</span>}
        title={<span id="cl-ledger-title">{t("clusters.ledger.title")}</span>}
        subtitle={t("clusters.ledger.subtitle")}
        actions={available ? (
          <Segmented
            label={t("clusters.ledger.window")}
            value={days}
            onChange={onDaysChange}
            items={[
              { value: "1", label: t("clusters.ledger.day1") },
              { value: "7", label: t("clusters.ledger.day7") },
            ]}
          />
        ) : null}
      />
      {available && error ? (
        <p className="cl-inline-error" role="alert">
          <span className="material-symbols-outlined" aria-hidden>error</span>
          {error}
        </p>
      ) : null}
      {!available ? (
        <EmptyPanel compact icon="receipt_long" title={t("clusters.ledger.unavailable")} />
      ) : !loaded ? (
        <EmptyPanel compact loading title={t("clusters.ledger.loading")} />
      ) : pairs.length === 0 ? (
        <EmptyPanel compact icon="receipt_long" title={t("clusters.ledger.empty")} description={t(days === "1" ? "clusters.ledger.emptyHintDay" : "clusters.ledger.emptyHintWeek")} />
      ) : (
        <div className="cp-list cl-ledger" role="table" aria-labelledby="cl-ledger-title">
          <div className="cp-list-head" role="row">
            <span role="columnheader">{t("clusters.ledger.columns.pair")}</span>
            <span role="columnheader" className="cl-num">{t("clusters.ledger.columns.requests")}</span>
            <span role="columnheader" className="cl-num">{t("clusters.ledger.columns.input")}</span>
            <span role="columnheader" className="cl-num">{t("clusters.ledger.columns.output")}</span>
            <span role="columnheader" className="cl-num">{t("clusters.ledger.columns.cost")}</span>
          </div>
          {pairs.map((pair) => (
            <div key={pair.key} role="row" className={cn("cp-list-row cl-ledger-row", (pair.lender === selfId || pair.borrower === selfId) && "is-self")}>
              <span role="cell" className="cp-cell cl-pair">
                <NodeRef name={pair.lenderName} id={pair.lender} self={pair.lender === selfId} />
                <span className="material-symbols-outlined" aria-label={t("clusters.ledger.lentTo")}>arrow_forward</span>
                <NodeRef name={pair.borrowerName} id={pair.borrower} self={pair.borrower === selfId} />
              </span>
              <span role="cell" className="cp-cell cl-num">{fmtInt(pair.requests, language)}</span>
              <span role="cell" className="cp-cell cl-num">{fmtInt(pair.input_tokens, language)}</span>
              <span role="cell" className="cp-cell cl-num">{fmtInt(pair.output_tokens, language)}</span>
              <span role="cell" className="cp-cell cl-num">{fmtUsd(pair.cost_usd, language)}</span>
            </div>
          ))}
          <div role="row" className="cp-list-row cl-ledger-total">
            <span role="cell" className="cp-cell">{t("clusters.ledger.total", { count: pairs.length })}</span>
            <span role="cell" className="cp-cell cl-num">{fmtInt(totals.requests, language)}</span>
            <span role="cell" className="cp-cell cl-num">{fmtInt(totals.input_tokens, language)}</span>
            <span role="cell" className="cp-cell cl-num">{fmtInt(totals.output_tokens, language)}</span>
            <span role="cell" className="cp-cell cl-num">{fmtUsd(totals.cost_usd, language)}</span>
          </div>
        </div>
      )}
    </section>
  );
}
