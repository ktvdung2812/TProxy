import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { IconAction, StatusPill } from "../page-kit";
import { ProviderLogo } from "../providers/ProviderLogo";
import { Modal } from "../ui";
import type { UsageEvent } from "./api";
import { fmt, fmtCostPrecise, fmtMs } from "./utils";

type Props = {
  event: UsageEvent | null;
  onClose: () => void;
  providerName: string;
  providerType: string;
  accountLabel: string;
  keyLabel: string;
};

function Facts({ title, icon, children }: { title: string; icon: string; children: ReactNode }) {
  return (
    <section className="us-detail-group">
      <h4>
        <span className="material-symbols-outlined" aria-hidden>{icon}</span>
        {title}
      </h4>
      <dl className="cp-facts">{children}</dl>
    </section>
  );
}

function Fact({ label, children, mono, title }: { label: string; children: ReactNode; mono?: boolean; title?: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className={mono ? "us-mono" : undefined} title={title ?? (typeof children === "string" ? children : undefined)}>{children}</dd>
    </div>
  );
}

/** Everything recorded for one upstream attempt, grouped by what an operator asks first. */
export function RequestDetailModal({ event, onClose, providerName, providerType, accountLabel, keyLabel }: Props) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  if (!event) return null;
  const ok = event.status >= 200 && event.status < 400;

  const copyId = () => {
    void navigator.clipboard
      ?.writeText(event.request_id)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => undefined);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("usage.requestDetails")}
      subtitle={<code className="us-mono">{event.request_id}</code>}
      icon="receipt_long"
      size="lg"
      headerAction={<IconAction icon={copied ? "check" : "content_copy"} label={copied ? t("common.copied") : t("usage.request.copyId")} onClick={copyId} />}
    >
      <div className="us-detail">
        <div className="us-detail-hero">
          <ProviderLogo className="us-logo" providerId={event.provider_id} providerType={providerType} />
          <div className="cp-cell-main">
            <strong>{event.public_model_id || event.upstream_model}</strong>
            <small>{providerName} · {accountLabel || "—"}</small>
          </div>
          <StatusPill tone={ok ? "success" : "danger"}>
            {ok ? t("usage.request.ok", { status: event.status }) : t("usage.request.failed", { status: event.status || t("usage.errorStatus") })}
          </StatusPill>
        </div>
        {!ok && event.error_code ? (
          <p className="us-detail-error" role="note">
            <span className="material-symbols-outlined" aria-hidden>error</span>
            <span>{t("usage.errorCode")}</span>
            <code>{event.error_code}</code>
          </p>
        ) : null}

        <Facts title={t("usage.request.routing")} icon="alt_route">
          <Fact label={t("usage.publicModel")} mono>{event.public_model_id || "—"}</Fact>
          <Fact label={t("usage.upstreamModel")} mono>{event.upstream_model || "—"}</Fact>
          <Fact label={t("usage.provider")}>{providerName}</Fact>
          <Fact label={t("usage.credential")}>{accountLabel || "—"}</Fact>
          <Fact label={t("usage.apiKey")}>{keyLabel}</Fact>
          <Fact label={t("usage.attempt")}>{String(event.attempt)}</Fact>
          <Fact label={t("operations.reason")}>{event.routing_reason || "—"}</Fact>
          <Fact label={t("usage.timestamp")}>{new Date(event.created_at).toLocaleString()}</Fact>
        </Facts>

        <Facts title={t("usage.request.timing")} icon="timer">
          <Fact label={t("usage.latency")} mono>{fmtMs(event.latency_ms)}</Fact>
          <Fact label={t("operations.firstToken")} mono>{fmtMs(event.ttft_ms)}</Fact>
          <Fact label={t("operations.queue")} mono>{fmtMs(event.queue_ms ?? 0)}</Fact>
        </Facts>

        <Facts title={t("usage.request.tokens")} icon="token">
          <Fact label={t("usage.inputTokensDetail")} mono>{fmt(event.input_tokens)}</Fact>
          <Fact label={t("usage.outputTokensDetail")} mono>{fmt(event.output_tokens)}</Fact>
          <Fact label={t("usage.reasoningTokens")} mono>{fmt(event.reasoning_tokens)}</Fact>
          <Fact label={t("usage.cachedTokens")} mono>{fmt(event.cached_tokens)}</Fact>
          <Fact label={t("operations.cacheWrite")} mono>{fmt(event.cache_creation_tokens ?? 0)}</Fact>
          <Fact label={t("usage.tokensSaved")} mono>{fmt(event.tokens_saved)}</Fact>
          <Fact label={t("usage.estimatedCost")} mono>{fmtCostPrecise(event.estimated_cost_usd)}</Fact>
        </Facts>
      </div>
    </Modal>
  );
}
