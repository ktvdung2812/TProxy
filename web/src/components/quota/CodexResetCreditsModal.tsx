import { useEffect, useState } from "react";
import { Button, Modal } from "../ui";
import { fetchCodexResetCredits, type CodexResetCredits } from "./api";

type CredentialRef = {
  id: string;
  label?: string;
  email?: string;
};

type Props = {
  open: boolean;
  secret: string;
  credential: CredentialRef | null;
  onClose: () => void;
};

function formatCreditDate(value?: string) {
  if (!value) return "N/A";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function formatTimeRemaining(value?: string) {
  if (!value) return "N/A";
  const diffMs = new Date(value).getTime() - Date.now();
  if (!Number.isFinite(diffMs)) return "N/A";
  if (diffMs <= 0) return "Expired";
  const totalHours = Math.ceil(diffMs / (60 * 60 * 1000));
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return days > 0 ? `${days}d ${hours}h` : `${hours}h`;
}

export function CodexResetCreditsModal({ open, secret, credential, onClose }: Props) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [data, setData] = useState<CodexResetCredits | null>(null);

  useEffect(() => {
    if (!open || !credential) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    setData(null);
    fetchCodexResetCredits(secret, credential.id)
      .then((result) => {
        if (!cancelled) setData(result);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : "Failed to load reset credits");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, secret, credential]);

  const label = credential?.label || credential?.email || credential?.id || "Codex account";

  return (
    <Modal open={open} onClose={onClose} title="Codex Reset Credit Expiry" subtitle={label} size="lg">
      {loading ? (
        <div className="quota-reset-modal-state">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          <span>Loading reset credits...</span>
        </div>
      ) : error ? (
        <div className="quota-reset-modal-error">{error}</div>
      ) : data?.credits?.length ? (
        <div className="quota-reset-modal-body">
          <div className="quota-reset-modal-summary">
            <span>{data.credits.length} reset credit{data.credits.length === 1 ? "" : "s"}</span>
            <span>{data.available_count ?? 0} available</span>
          </div>
          <div className="quota-reset-modal-table-wrap">
            <table className="quota-reset-modal-table">
              <thead>
                <tr>
                  <th>Status</th>
                  <th>Granted At</th>
                  <th>Expires At</th>
                  <th>Remaining</th>
                </tr>
              </thead>
              <tbody>
                {data.credits.map((credit, index) => (
                  <tr key={`${credit.status}-${credit.expires_at || index}`}>
                    <td>
                      <span className="quota-reset-status">{credit.status || "unknown"}</span>
                    </td>
                    <td>{formatCreditDate(credit.granted_at)}</td>
                    <td>{formatCreditDate(credit.expires_at)}</td>
                    <td>{formatTimeRemaining(credit.expires_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="quota-reset-modal-state">No reset credit details returned for this account.</div>
      )}
      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 16 }}>
        <Button variant="secondary" onClick={onClose}>Close</Button>
      </div>
    </Modal>
  );
}
