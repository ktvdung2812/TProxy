import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, cn } from "../ui";
import { EmptyPanel, IconAction, SectionHead, StatusPill } from "../page-kit";
import { formatAgo } from "../upstreams/health";
import { listJoinTokens } from "./api";
import type { JoinToken } from "./types";
import { TOKEN_TONE, clusterErrorMessage, fmtDateTime, fmtInt, isAbortError, tokenState, type TokenState } from "./utils";

type Props = {
  secret: string;
  /** Changes after each status load or mutation, so the list follows them. */
  refreshKey: number;
  now: number;
  busy: string | null;
  onCreate: () => void;
  onRevoke: (token: JoinToken) => void;
};

const STATE_RANK: Record<TokenState, number> = { active: 0, usedUp: 1, expired: 2, revoked: 3 };

/** Hub only: who may join, how often and until when. */
export function ClusterTokens({ secret, refreshKey, now, busy, onCreate, onRevoke }: Props) {
  const { t, i18n } = useTranslation();
  const [tokens, setTokens] = useState<JoinToken[] | null>(null);
  const [loadError, setLoadError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    listJoinTokens(secret, controller.signal)
      .then((list) => {
        if (controller.signal.aborted) return;
        setTokens(list);
        setLoadError("");
      })
      .catch((cause) => {
        if (controller.signal.aborted || isAbortError(cause)) return;
        setLoadError(clusterErrorMessage(cause, t, t("clusters.tokens.loadFailed")));
      });
    return () => controller.abort();
  }, [secret, refreshKey, t]);

  const rows = useMemo(
    () =>
      (tokens || [])
        .map((token) => ({ token, state: tokenState(token, now) }))
        .sort(
          (left, right) =>
            STATE_RANK[left.state] - STATE_RANK[right.state]
            || (Date.parse(right.token.created_at) || 0) - (Date.parse(left.token.created_at) || 0),
        ),
    [tokens, now],
  );
  const activeCount = rows.filter((row) => row.state === "active").length;

  return (
    <section className="cl-section" aria-labelledby="cl-tokens-title">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>key</span>}
        title={<span id="cl-tokens-title">{t("clusters.tokens.title")}</span>}
        subtitle={tokens ? t("clusters.tokens.summary", { count: rows.length, active: activeCount }) : t("clusters.tokens.subtitle")}
        actions={
          <Button size="sm" icon="add" onClick={onCreate} disabled={busy !== null}>
            {t("clusters.tokens.create")}
          </Button>
        }
      />
      {tokens === null ? (
        loadError ? (
          <EmptyPanel compact icon="cloud_off" title={t("clusters.tokens.loadFailed")} description={<span role="alert">{loadError}</span>} />
        ) : (
          <EmptyPanel compact loading title={t("clusters.tokens.loading")} />
        )
      ) : rows.length === 0 ? (
        <EmptyPanel
          compact
          icon="key"
          title={t("clusters.tokens.empty")}
          description={t("clusters.tokens.emptyHint")}
          action={<Button variant="secondary" icon="add" onClick={onCreate} disabled={busy !== null}>{t("clusters.tokens.create")}</Button>}
        />
      ) : (
        <div className="cp-list cl-tokens" role="table" aria-label={t("clusters.tokens.title")}>
          <div className="cp-list-head" role="row">
            <span role="columnheader">{t("clusters.tokens.columns.label")}</span>
            <span role="columnheader">{t("clusters.tokens.columns.status")}</span>
            <span role="columnheader">{t("clusters.tokens.columns.uses")}</span>
            <span role="columnheader">{t("clusters.tokens.columns.expires")}</span>
            <span role="columnheader">{t("clusters.tokens.columns.created")}</span>
            <span role="columnheader" aria-label={t("clusters.tokens.columns.actions")} />
          </div>
          {rows.map(({ token, state }) => {
            const label = token.label || t("clusters.tokens.unlabeled");
            const expires = state === "revoked" ? null : formatAgo(token.expires_at, i18n.language, now);
            const revoking = busy === `token:${token.id}`;
            return (
              <div
                key={token.id}
                role="row"
                className={cn("cp-list-row cl-token-row", state !== "active" && "is-off")}
                style={{ ["--cp-row-tone" as string]: state === "active" ? "var(--color-success)" : "transparent" }}
              >
                <span role="cell" className="cp-cell cp-cell-main">
                  <strong title={label} className={cn(!token.label && "cl-dim")}>{label}</strong>
                  <small title={token.id}>{token.id}</small>
                </span>
                <span role="cell" className="cp-cell cp-keep-bright">
                  <StatusPill tone={TOKEN_TONE[state]}>{t(`clusters.tokens.state.${state}`)}</StatusPill>
                </span>
                <span role="cell" className="cp-cell cp-cell-mono">
                  {token.max_uses > 0
                    ? t("clusters.tokens.usesOf", { uses: fmtInt(token.uses, i18n.language), max: fmtInt(token.max_uses, i18n.language) })
                    : t("clusters.tokens.usesUnlimited", { uses: fmtInt(token.uses, i18n.language) })}
                </span>
                <span role="cell" className="cp-cell cp-cell-mono" title={fmtDateTime(token.expires_at, i18n.language) || undefined}>
                  {state === "revoked"
                    ? t("clusters.tokens.revokedAgo", { time: formatAgo(token.revoked_at, i18n.language, now) || "—" })
                    : expires || "—"}
                </span>
                <span role="cell" className="cp-cell cp-cell-mono" title={fmtDateTime(token.created_at, i18n.language) || undefined}>
                  {formatAgo(token.created_at, i18n.language, now) || "—"}
                </span>
                <span role="cell" className="cp-cell cp-cell-actions cp-keep-bright">
                  {state === "active" ? (
                    <IconAction
                      icon="block"
                      danger
                      label={t("clusters.tokens.revoke")}
                      spinning={revoking}
                      disabled={busy !== null}
                      onClick={() => onRevoke(token)}
                    />
                  ) : null}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
