import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "../ui";
import { fetchCodexResetCredits, type CodexResetCredit, type CodexResetCredits } from "./api";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";

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
  onUse?: (credit: CodexResetCredit) => void;
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

function formatTimeRemaining(value: string | undefined, missingLabel: string, expiredLabel: string) {
  if (!value) return missingLabel;
  const diffMs = new Date(value).getTime() - Date.now();
  if (!Number.isFinite(diffMs)) return missingLabel;
  if (diffMs <= 0) return expiredLabel;
  const totalHours = Math.ceil(diffMs / (60 * 60 * 1000));
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return days > 0 ? `${days}d ${hours}h` : `${hours}h`;
}

function canUseCredit(credit: CodexResetCredit) {
  if (credit.status.trim().toLowerCase() !== "available") return false;
  if (!credit.expires_at) return true;
  const expiresAt = new Date(credit.expires_at).getTime();
  return Number.isFinite(expiresAt) && expiresAt > Date.now();
}

export function CodexResetCreditsModal({ open, secret, credential, onClose, onUse }: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [data, setData] = useState<CodexResetCredits | null>(null);
  const [, setClock] = useState(0);

  const credits = useMemo(
    () => [...(data?.credits || [])].sort((a, b) => {
      const left = a.expires_at ? new Date(a.expires_at).getTime() : Number.POSITIVE_INFINITY;
      const right = b.expires_at ? new Date(b.expires_at).getTime() : Number.POSITIVE_INFINITY;
      return left - right;
    }),
    [data],
  );

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

  useEffect(() => {
    if (!open) return;
    const timer = window.setInterval(() => setClock((value) => value + 1), 60000);
    return () => window.clearInterval(timer);
  }, [open]);

  const label = maskAccountText(credential?.label || credential?.email || credential?.id || "Codex account", masked);

  return (
    <Modal open={open} onClose={onClose} title={t("quota.codexResetCreditExpiry")} subtitle={label} size="lg">
      {loading ? (
        <div className="quota-reset-modal-state">
          <span className="material-symbols-outlined animate-spin">progress_activity</span>
          <span>{t("quota.loadingResetCredits")}</span>
        </div>
      ) : error ? (
        <div className="quota-reset-modal-error">{error}</div>
      ) : credits.length ? (
        <div className="quota-reset-modal-body">
          <div className="quota-reset-modal-summary">
            <span>{credits.length} {credits.length === 1 ? t("quota.resetCredit") : t("quota.resetCredits")}</span>
            <span>{data?.available_count ?? 0} {t("quota.available")}</span>
          </div>
          <div className="quota-reset-modal-table-wrap">
            <table className="quota-reset-modal-table">
              <thead>
                <tr>
                  <th>{t("quota.tableStatus")}</th>
                  <th>{t("quota.grantedAt")}</th>
                  <th>{t("quota.expiresAt")}</th>
                  <th>{t("quota.remaining")}</th>
                  <th>{t("quota.tableAction")}</th>
                </tr>
              </thead>
              <tbody>
                {credits.map((credit, index) => (
                  <tr key={credit.id || `${credit.status}-${credit.expires_at || index}`}>
                    <td>
                      <span className="quota-reset-status">{credit.status || "unknown"}</span>
                    </td>
                    <td>{formatCreditDate(credit.granted_at)}</td>
                    <td>{formatCreditDate(credit.expires_at)}</td>
                    <td>{formatTimeRemaining(credit.expires_at, t("quota.notAvailable"), t("quota.expired"))}</td>
                    <td>
                      {onUse && canUseCredit(credit) ? (
                        <button
                          type="button"
                          className="quota-reset-use-btn"
                          onClick={() => {
                            onUse(credit);
                            onClose();
                          }}
                        >
                          {t("quota.useResetCreditAction")}
                        </button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <div className="quota-reset-modal-state">{t("quota.noResetCreditDetails")}</div>
      )}
      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 16 }}>
        <Button variant="secondary" onClick={onClose}>{t("common.close")}</Button>
      </div>
    </Modal>
  );
}
