import { useState } from "react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { RequestLog } from "../../hooks/useRequestLogStream";
import { StatusPill } from "../page-kit";
import { Button, Modal } from "../ui";
import type { AuditEvent } from "./api";
import { keyLabel, type RequestLogResolvers } from "./RequestLogList";
import { fmtAbsolute, fmtLatency, modelOf, protocolKey, providerOf, statusTone } from "./utils";

function Fact({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd className={mono ? "lg-mono" : undefined} title={typeof children === "string" ? children : undefined}>
        {children || "—"}
      </dd>
    </div>
  );
}

function useCopy() {
  const [copied, setCopied] = useState<string | null>(null);
  const copy = async (key: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      window.setTimeout(() => setCopied((current) => (current === key ? null : current)), 1600);
    } catch {
      /* clipboard may be unavailable */
    }
  };
  return { copied, copy };
}

function MetadataBlock({ metadata }: { metadata?: Record<string, unknown> }) {
  const { t } = useTranslation();
  if (!metadata || Object.keys(metadata).length === 0) return null;
  return (
    <section className="lg-detail-section">
      <h4>{t("logs.detail.metadata")}</h4>
      <pre className="lg-json">{JSON.stringify(metadata, null, 2)}</pre>
    </section>
  );
}

type RequestProps = {
  log: RequestLog | null;
  resolvers?: RequestLogResolvers;
  onClose: () => void;
  onFilter: (query: string) => void;
  onOpenProvider: (providerId: string) => void;
};

export function RequestLogDetailModal({ log, resolvers, onClose, onFilter, onOpenProvider }: RequestProps) {
  const { t } = useTranslation();
  const { copied, copy } = useCopy();
  if (!log) return null;
  const model = modelOf(log);
  const provider = providerOf(log);
  const account = log.credential_id ? resolvers?.account?.(log.credential_id) || log.credential_id : "";
  const tone = statusTone(log.status);
  return (
    <Modal
      open
      onClose={onClose}
      size="md"
      icon="receipt_long"
      title={`${log.method} ${log.path}`}
      subtitle={fmtAbsolute(log.created_at)}
      footer={
        <>
          {provider ? (
            <Button variant="ghost" size="sm" icon="dns" onClick={() => onOpenProvider(provider)}>
              {t("logs.detail.openProvider")}
            </Button>
          ) : null}
          <Button variant="secondary" size="sm" icon={copied === "json" ? "check" : "data_object"} onClick={() => void copy("json", JSON.stringify(log, null, 2))}>
            {copied === "json" ? t("common.copied") : t("logs.detail.copyJson")}
          </Button>
          <Button variant="primary" size="sm" onClick={onClose}>{t("common.close")}</Button>
        </>
      }
    >
      <div className="lg-detail">
        <div className="lg-detail-status">
          <StatusPill tone={tone}>{log.status || "—"}</StatusPill>
          {log.error_code ? <code className={`cp-tone-${tone}`}>{log.error_code}</code> : null}
          <span className="lg-mono">{fmtLatency(log.latency_ms)}</span>
        </div>
        <dl className="cp-facts">
          <Fact label={t("logs.detail.requestId")} mono>{log.request_id}</Fact>
          <Fact label={t("logs.detail.protocol")} mono>{protocolKey(log) === "other" ? "" : protocolKey(log)}</Fact>
          <Fact label={t("logs.detail.model")} mono>{model}</Fact>
          <Fact label={t("logs.detail.provider")} mono>{provider}</Fact>
          <Fact label={t("logs.detail.account")} mono>{account}</Fact>
          <Fact label={t("logs.detail.attempt")} mono>{log.attempt ? String(log.attempt) : ""}</Fact>
          <Fact label={t("logs.detail.apiKey")} mono>{keyLabel(log, resolvers)}</Fact>
          <Fact label={t("logs.detail.latency")} mono>{`${log.latency_ms ?? 0} ms`}</Fact>
        </dl>
        <div className="lg-detail-actions">
          <Button variant="outline" size="sm" icon={copied === "id" ? "check" : "content_copy"} onClick={() => void copy("id", log.request_id)}>
            {copied === "id" ? t("common.copied") : t("logs.copyRequestId")}
          </Button>
          {model ? (
            <Button variant="outline" size="sm" icon="filter_alt" onClick={() => { onFilter(model); onClose(); }}>
              {t("logs.detail.filterModel")}
            </Button>
          ) : null}
          {log.client_api_key_id ? (
            <Button variant="outline" size="sm" icon="key" onClick={() => { onFilter(log.client_api_key_id || ""); onClose(); }}>
              {t("logs.detail.filterKey")}
            </Button>
          ) : null}
        </div>
        <MetadataBlock metadata={log.metadata} />
      </div>
    </Modal>
  );
}

type AuditProps = {
  event: AuditEvent | null;
  onClose: () => void;
  onFilter: (query: string) => void;
};

export function AuditDetailModal({ event, onClose, onFilter }: AuditProps) {
  const { t } = useTranslation();
  const { copied, copy } = useCopy();
  if (!event) return null;
  const tone = statusTone(event.status);
  return (
    <Modal
      open
      onClose={onClose}
      size="md"
      icon="history"
      title={event.action}
      subtitle={fmtAbsolute(event.created_at)}
      footer={
        <>
          <Button variant="secondary" size="sm" icon={copied === "json" ? "check" : "data_object"} onClick={() => void copy("json", JSON.stringify(event, null, 2))}>
            {copied === "json" ? t("common.copied") : t("logs.detail.copyJson")}
          </Button>
          <Button variant="primary" size="sm" onClick={onClose}>{t("common.close")}</Button>
        </>
      }
    >
      <div className="lg-detail">
        <div className="lg-detail-status">
          <StatusPill tone={tone}>{event.status || "—"}</StatusPill>
        </div>
        <dl className="cp-facts">
          <Fact label={t("logs.auditView.action")} mono>{event.action}</Fact>
          <Fact label={t("logs.auditView.resourceType")} mono>{event.resource_type || ""}</Fact>
          <Fact label={t("logs.auditView.resourceId")} mono>{event.resource_id || ""}</Fact>
          <Fact label={t("logs.actor")} mono>{event.actor || ""}</Fact>
        </dl>
        {event.resource_id ? (
          <div className="lg-detail-actions">
            <Button variant="outline" size="sm" icon="filter_alt" onClick={() => { onFilter(event.resource_id || ""); onClose(); }}>
              {t("logs.auditView.filterResource")}
            </Button>
          </div>
        ) : null}
        <MetadataBlock metadata={event.metadata} />
      </div>
    </Modal>
  );
}
