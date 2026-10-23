import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Button, cn } from "../ui";
import { EmptyPanel, LinkButton, SectionHead, Tag, ToneCounts, ToolButton } from "../page-kit";
import type { AttentionItem } from "./attention";

const PREVIEW = 6;

type Props = {
  items: AttentionItem[];
  loading?: boolean;
  /** No provider is configured yet: the first step is to add one, not to fix anything. */
  empty?: boolean;
};

export function AttentionPanel({ items, loading, empty }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(false);
  const shown = expanded ? items : items.slice(0, PREVIEW);
  const count = (tone: string) => items.filter((item) => item.tone === tone).length;

  let body;
  if (loading && items.length === 0) {
    body = (
      <div className="cp-skeleton ov-attention-skeleton" aria-label={t("common.loading")}>
        <span />
        <span />
        <span />
      </div>
    );
  } else if (empty) {
    body = (
      <EmptyPanel
        compact
        icon="add_link"
        title={t("overview.attention.noProviders")}
        description={t("overview.attention.noProvidersHint")}
        action={<Button variant="primary" size="sm" icon="add_link" onClick={() => navigate("/providers")}>{t("overview.connectProvider")}</Button>}
      />
    );
  } else if (items.length === 0) {
    body = (
      <EmptyPanel compact icon="verified" title={t("overview.attention.allClear")} description={t("overview.attention.allClearHint")} />
    );
  } else {
    body = (
      <ul className="ov-attention-list">
        {shown.map((item) => (
          <li key={item.key} className={cn("ov-attention-item", `cp-tone-${item.tone}`)}>
            <span className="material-symbols-outlined ov-attention-icon" aria-hidden>{item.icon}</span>
            <div className="ov-attention-copy">
              <strong>{item.title}</strong>
              {item.detail ? <small title={item.detail}>{item.detail}</small> : null}
            </div>
            <span className="ov-attention-kind">
              <Tag tone="neutral">{t(`overview.attention.kind.${item.kind}`)}</Tag>
            </span>
            <ToolButton icon="arrow_forward" tone={item.tone === "danger" ? "danger" : undefined} onClick={() => navigate(item.to)}>
              {item.action}
            </ToolButton>
          </li>
        ))}
      </ul>
    );
  }

  return (
    <section className="cp-panel ov-panel ov-attention" aria-label={t("overview.attention.title")}>
      <SectionHead
        leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>notification_important</span>}
        title={t("overview.attention.title")}
        subtitle={
          items.length > 0
            ? t("overview.attention.summary", { count: items.length })
            : t("overview.attention.summaryNone")
        }
        meta={
          <ToneCounts
            label={t("overview.attention.title")}
            items={[
              { tone: "danger", count: count("danger"), label: t("overview.attention.toneDanger") },
              { tone: "warning", count: count("warning"), label: t("overview.attention.toneWarning") },
              { tone: "muted", count: count("muted") + count("info"), label: t("overview.attention.toneInfo") },
            ]}
          />
        }
      />
      {body}
      {items.length > PREVIEW ? (
        <div className="ov-panel-foot">
          <LinkButton onClick={() => setExpanded((value) => !value)}>
            {expanded ? t("overview.attention.showFewer") : t("overview.attention.showAll", { count: items.length })}
          </LinkButton>
        </div>
      ) : null}
    </section>
  );
}
