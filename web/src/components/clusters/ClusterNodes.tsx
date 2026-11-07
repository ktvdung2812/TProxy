import { Fragment, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { cn } from "../ui";
import { ActionMenu, EmptyPanel, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import { formatAgo } from "../upstreams/health";
import type { ClusterNode, ClusterRole } from "./types";
import { NODE_TONE, fmtDateTime, fmtInt, nodeState, type NodeState } from "./utils";

type Props = {
  nodes: ClusterNode[];
  /** Only the hub may edit or revoke other nodes. */
  role: ClusterRole;
  now: number;
  busy: string | null;
  onEdit: (node: ClusterNode) => void;
  onRevoke: (node: ClusterNode) => void;
  onRenameSelf: () => void;
};

const STATE_RANK: Record<NodeState, number> = { online: 0, offline: 1, revoked: 2, left: 3 };

function menuItems(node: ClusterNode, props: Props, t: TFunction): ActionMenuItem[] {
  const busy = props.busy !== null;
  if (node.self) {
    return [{ key: "rename", icon: "badge", label: t("clusters.nodes.renameSelf"), hint: t("clusters.nodes.renameSelfHint"), disabled: busy, onSelect: props.onRenameSelf }];
  }
  if (props.role !== "hub") return [];
  const active = node.status !== "revoked" && node.status !== "left";
  return [
    { key: "edit", icon: "edit", label: t("clusters.nodes.edit"), hint: t("clusters.nodes.editHint"), disabled: busy, onSelect: () => props.onEdit(node) },
    {
      key: "revoke",
      icon: "block",
      label: t("clusters.nodes.revoke"),
      hint: active ? t("clusters.nodes.revokeHint") : t("clusters.nodes.revokeDone"),
      danger: true,
      disabled: busy || !active,
      onSelect: () => props.onRevoke(node),
    },
  ];
}

export function ClusterNodes(props: Props) {
  const { nodes, now } = props;
  const { t, i18n } = useTranslation();
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());

  const sorted = useMemo(
    () =>
      [...nodes].sort(
        (left, right) =>
          Number(right.self) - Number(left.self)
          || Number(right.role === "hub") - Number(left.role === "hub")
          || STATE_RANK[nodeState(left)] - STATE_RANK[nodeState(right)]
          || (left.name || left.id).localeCompare(right.name || right.id),
      ),
    [nodes],
  );

  const toggle = (id: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  if (sorted.length === 0) {
    return <EmptyPanel compact icon="dns" title={t("clusters.nodes.empty")} description={t("clusters.nodes.emptyHint")} />;
  }

  return (
    <div className="cp-list cl-nodes" role="table" aria-label={t("clusters.tabs.nodes")}>
      <div className="cp-list-head" role="row">
        <span role="columnheader">{t("clusters.nodes.columns.node")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.owner")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.role")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.status")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.version")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.lastSeen")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.rtt")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.lending")}</span>
        <span role="columnheader">{t("clusters.nodes.columns.offers")}</span>
        <span role="columnheader" aria-label={t("clusters.nodes.columns.actions")} />
      </div>
      {sorted.map((node) => {
        const state = nodeState(node);
        const name = node.name || node.id;
        const open = expanded.has(node.id);
        const items = menuItems(node, props, t);
        const lastSeen = node.self ? null : formatAgo(node.last_seen_at, i18n.language, now);
        const offersId = `cl-offers-${node.id}`;
        return (
          <Fragment key={node.id}>
            <div
              role="row"
              className={cn("cp-list-row cl-node-row", (state === "revoked" || state === "left") && "is-off", node.self && "is-self")}
              style={{ ["--cp-row-tone" as string]: `var(--color-${NODE_TONE[state] === "neutral" ? "border" : NODE_TONE[state]})` }}
            >
              <span role="cell" className="cp-cell cp-cell-main">
                <span className="cl-node-name">
                  <strong title={name}>{name}</strong>
                  {node.self ? <Tag tone="primary">{t("clusters.nodes.thisNode")}</Tag> : null}
                </span>
                <small title={node.id}>{node.id}</small>
              </span>
              <span role="cell" className={cn("cp-cell cp-cell-mono", !node.owner && "cl-dim")} title={node.owner || undefined}>
                {node.owner || "—"}
              </span>
              <span role="cell" className="cp-cell">
                <Tag tone={node.role === "hub" ? "primary" : "neutral"}>{t(`clusters.role.${node.role}`)}</Tag>
              </span>
              <span role="cell" className="cp-cell cp-keep-bright">
                <StatusPill tone={NODE_TONE[state]} title={t(`clusters.nodes.stateHint.${state}`)}>{t(`clusters.nodes.state.${state}`)}</StatusPill>
              </span>
              <span role="cell" className={cn("cp-cell cp-cell-mono", !node.version && "cl-dim")}>{node.version || "—"}</span>
              <span role="cell" className="cp-cell cp-cell-mono" title={fmtDateTime(node.last_seen_at, i18n.language) || undefined}>
                {node.self ? t("clusters.nodes.now") : lastSeen || "—"}
              </span>
              <span role="cell" className="cp-cell cp-cell-mono">
                {!node.self && node.rtt_ms > 0 ? t("clusters.ms", { value: fmtInt(node.rtt_ms, i18n.language) }) : "—"}
              </span>
              <span role="cell" className="cp-cell">
                <StatusPill dot={false} tone={node.lending_enabled ? "success" : "muted"}>
                  {node.lending_enabled ? t("clusters.nodes.lendingOn") : t("clusters.nodes.lendingOff")}
                </StatusPill>
              </span>
              <span role="cell" className="cp-cell cp-keep-bright" title={t("clusters.nodes.offerHealthHint")}>
                <button
                  type="button"
                  className="cl-offers-btn"
                  aria-expanded={open}
                  aria-controls={open ? offersId : undefined}
                  disabled={node.offers.length === 0}
                  onClick={() => toggle(node.id)}
                  title={t("clusters.nodes.offerHealthHint")}
                >
                  <span>{node.offers.filter((offer) => offer.eligible_accounts > 0).length}/{node.offers.length}</span>
                  {node.offers.length ? <span className="material-symbols-outlined" aria-hidden>{open ? "expand_less" : "expand_more"}</span> : null}
                </button>
              </span>
              <span role="cell" className="cp-cell cp-cell-actions cp-keep-bright">
                {items.length ? <ActionMenu label={t("clusters.nodes.actionsFor", { name })} items={items} /> : null}
              </span>
            </div>
            {open && node.offers.length ? (
              <div id={offersId} className="cl-node-offers" role="row">
                <span role="cell">
                  <span className="cl-offers-title">{t("clusters.nodes.offersTitle", { name })}</span>
                  <ul className="cl-offer-list">
                    {[...node.offers]
                      .sort((left, right) => left.model_id.localeCompare(right.model_id))
                      .map((offer) => (
                        <li
                          key={offer.model_id}
                          className={cn("cl-offer", offer.eligible_accounts > 0 ? "cp-tone-success" : "cp-tone-muted", offer.eligible_accounts <= 0 && "is-unhealthy")}
                          title={t("clusters.nodes.offerHealthHint")}
                        >
                          <span className="cl-offer-dot" aria-hidden />
                          <code>{offer.model_id}</code>
                          <small>{offer.eligible_accounts > 0 ? t("clusters.nodes.accounts", { count: offer.eligible_accounts }) : t("clusters.nodes.noAccount")}</small>
                        </li>
                      ))}
                  </ul>
                </span>
              </div>
            ) : null}
          </Fragment>
        );
      })}
    </div>
  );
}
