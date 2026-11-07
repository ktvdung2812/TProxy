import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { fetchTopologyClientDetail, type TopologyClientDetail } from "./api";
import { formatNumber, timeAgo } from "./utils";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";

type Props = {
  secret: string;
  clientKeyId: string;
  clientLabel: string;
  onClose: () => void;
};

export function ClientDetailModal({ secret, clientKeyId, clientLabel, onClose }: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const [detail, setDetail] = useState<TopologyClientDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    fetchTopologyClientDetail(secret, clientKeyId)
      .then((data) => {
        if (!cancelled) setDetail(data);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : t("topology.failedLoadClient"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, clientKeyId, t]);

  return (
    <div className="topology-modal-backdrop" onClick={onClose}>
      <div className="topology-modal" role="dialog" aria-modal="true" aria-label={clientLabel} onClick={(event) => event.stopPropagation()}>
        <div className="topology-modal-head">
          <div>
            <strong>{clientLabel}</strong>
            <p className="muted">{clientKeyId}</p>
          </div>
          <button type="button" className="topology-modal-close" aria-label={t("common.close")} onClick={onClose}>×</button>
        </div>

        {loading ? (
          <div className="topology-modal-loading" role="status" aria-label={t("common.loading")}>
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
          </div>
        ) : null}

        {error ? <div className="topology-modal-error">{error}</div> : null}

        {detail ? (
          <>
            <div className="topology-modal-stats">
              <div>
                <span>{formatNumber(detail.summary.total_requests)}</span>
                <small>{t("topology.totalRequests")}</small>
              </div>
              <div>
                <span>{formatNumber(detail.summary.today_requests)}</span>
                <small>{t("topology.today")}</small>
              </div>
              <div>
                <span>{detail.summary.last_seen_at ? timeAgo(detail.summary.last_seen_at) : "—"}</span>
                <small>{t("topology.lastSeen")}</small>
              </div>
              <div>
                <span>{detail.summary.first_seen_at ? timeAgo(detail.summary.first_seen_at) : "—"}</span>
                <small>{t("topology.firstSeen")}</small>
              </div>
            </div>

            <div className="topology-modal-table-wrap" role="region" aria-label={t("featureB.topology.modelUsage")} tabIndex={0}>
              <table className="topology-modal-table">
                <thead>
                  <tr>
                    <th>{t("featureB.topology.model")}</th>
                    <th>{t("featureB.topology.provider")}</th>
                    <th>{t("featureB.topology.credential")}</th>
                    <th className="right">{t("topology.requests")}</th>
                    <th className="right">{t("topology.lastUsed")}</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.models.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="muted">{t("featureB.topology.noModelUsage")}</td>
                    </tr>
                  ) : (
                    detail.models.map((row) => (
                      <tr key={`${row.model}-${row.provider}-${row.credential_id}`}>
                        <td>{row.model}</td>
                        <td>{row.provider}</td>
                        <td>{maskAccountText(row.account_label || row.credential_id, masked)}</td>
                        <td className="right">{formatNumber(row.request_count)}</td>
                        <td className="right muted">{row.last_used_at ? timeAgo(row.last_used_at) : "—"}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}
