import { useEffect, useState } from "react";
import { fetchTopologyClientDetail, type TopologyClientDetail } from "./api";
import { formatNumber, timeAgo } from "./utils";

type Props = {
  secret: string;
  clientKeyId: string;
  clientLabel: string;
  onClose: () => void;
};

export function ClientDetailModal({ secret, clientKeyId, clientLabel, onClose }: Props) {
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : "Failed to load client detail");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [secret, clientKeyId]);

  return (
    <div className="topology-modal-backdrop" onClick={onClose}>
      <div className="topology-modal" onClick={(event) => event.stopPropagation()}>
        <div className="topology-modal-head">
          <div>
            <strong>{clientLabel}</strong>
            <p className="muted">{clientKeyId}</p>
          </div>
          <button type="button" className="topology-modal-close" onClick={onClose}>×</button>
        </div>

        {loading ? (
          <div className="topology-modal-loading">
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
          </div>
        ) : null}

        {error ? <div className="topology-modal-error">{error}</div> : null}

        {detail ? (
          <>
            <div className="topology-modal-stats">
              <div>
                <span>{formatNumber(detail.summary.total_requests)}</span>
                <small>Total requests</small>
              </div>
              <div>
                <span>{formatNumber(detail.summary.today_requests)}</span>
                <small>Today</small>
              </div>
              <div>
                <span>{detail.summary.last_seen_at ? timeAgo(detail.summary.last_seen_at) : "—"}</span>
                <small>Last seen</small>
              </div>
              <div>
                <span>{detail.summary.first_seen_at ? timeAgo(detail.summary.first_seen_at) : "—"}</span>
                <small>First seen</small>
              </div>
            </div>

            <div className="topology-modal-table-wrap">
              <table className="topology-modal-table">
                <thead>
                  <tr>
                    <th>Model</th>
                    <th>Provider</th>
                    <th>Credential</th>
                    <th className="right">Requests</th>
                    <th className="right">Last used</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.models.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="muted">No model usage recorded for this client.</td>
                    </tr>
                  ) : (
                    detail.models.map((row) => (
                      <tr key={`${row.model}-${row.provider}-${row.credential_id}`}>
                        <td>{row.model}</td>
                        <td>{row.provider}</td>
                        <td>{row.account_label || row.credential_id}</td>
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
