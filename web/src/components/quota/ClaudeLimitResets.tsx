import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useState } from "react";
import { Button, cn } from "../ui";
import {
  claimClaudeLimitReset,
  fetchClaudeLimitResets,
  type ClaudeLimitResets as ClaudeLimitResetsData,
  type ClaudeResetGrant,
  type ClaudeResetProgram,
} from "./api";

type Props = {
  secret: string;
  credentialId: string;
  active: boolean;
  onReset?: () => void;
};

function formatDate(value: string | undefined, language: string, withTime = false) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(language, { month: "short", day: "numeric", ...(withTime ? { hour: "numeric", minute: "2-digit" } : {}) });
}

function grantUsable(data: ClaudeLimitResetsData, grant: ClaudeResetGrant) {
  return data.eligible && grant.usable_now && !grant.paused && grant.resets_left > 0;
}

export function ClaudeLimitResets({ secret, credentialId, active, onReset }: Props) {
  const { t, i18n } = useTranslation();
  const [data, setData] = useState<ClaudeLimitResetsData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [confirmKey, setConfirmKey] = useState<string | null>(null);
  const [claimingKey, setClaimingKey] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setData(await fetchClaudeLimitResets(secret, credentialId));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("featureA.loadResetsFailed"));
    } finally {
      setLoading(false);
    }
  }, [secret, credentialId, t]);

  useEffect(() => {
    setData(null);
    setNotice("");
    setConfirmKey(null);
    if (active) void load();
  }, [active, load]);

  const claim = async (key: string, program: ClaudeResetProgram, grantId?: string) => {
    setConfirmKey(null);
    setClaimingKey(key);
    setError("");
    setNotice("");
    try {
      const result = await claimClaudeLimitReset(secret, credentialId, program, grantId);
      if (result.ok) {
        setNotice(t("featureA.resetResults.reset"));
        onReset?.();
      } else {
        setError(t(`featureA.resetResults.${result.result}`, { defaultValue: t("featureA.resetNotApplied", { result: result.result }) }));
      }
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("featureA.resetFailed"));
    } finally {
      setClaimingKey(null);
    }
  };

  const renderAction = (key: string, enabled: boolean, program: ClaudeResetProgram, grantId?: string) => {
    if (confirmKey === key) {
      return (
        <div className="claude-limit-reset-actions" style={{ flexWrap: "wrap" }}>
          <Button variant="secondary" size="sm" onClick={() => setConfirmKey(null)}>{t("common.cancel")}</Button>
          <Button variant="danger" size="sm" onClick={() => void claim(key, program, grantId)}>{t("featureA.confirmReset")}</Button>
        </div>
      );
    }
    return (
      <Button
        variant="secondary"
        size="sm"
        disabled={!enabled || claimingKey !== null}
        loading={claimingKey === key}
        onClick={() => setConfirmKey(key)}
      >
        {t("featureA.resetForFree")}
      </Button>
    );
  };

  const session = data?.session;
  const sessionKey = "session";

  return (
    <div className="quota-account-modal-section">
      <div className="quota-account-modal-section-head">
        <div className="quota-account-modal-section-head-main">
          <h5>{t("featureA.limitResets")}</h5>
          <span className="quota-account-modal-section-meta">{t("featureA.limitResetsHint")}</span>
        </div>
        <button
          type="button"
          className="quota-tracker-icon-btn"
          disabled={loading}
          onClick={() => void load()}
          aria-label={t("featureA.refreshResets")}
          title={t("featureA.refreshResets")}
        >
          <span className={cn("material-symbols-outlined", loading && "animate-spin")}>refresh</span>
        </button>
      </div>
      {error ? <p className="quota-account-modal-note quota-account-modal-note-error">{error}</p> : null}
      {notice ? <p className="quota-account-modal-note">{notice}</p> : null}
      {loading && !data ? (
        <p className="quota-account-modal-empty">{t("featureA.loadingResets")}</p>
      ) : data ? (
        <div className="claude-limit-reset-list">
          {!data.eligible && data.ineligible_reason ? (
            <p className="quota-account-modal-note">{t("featureA.ineligibleReason", { reason: data.ineligible_reason.replace(/_/g, " ")})}</p>
          ) : null}
          {data.grants.map((grant) => {
            const key = `grant:${grant.id}`;
            return (
              <div key={grant.id} className="claude-limit-reset-row">
                <div className="claude-limit-reset-copy">
                  <strong title={grant.label || grant.id}>{t("featureA.fullReset")}</strong>
                  <span>{[grant.ends_at && t("featureA.expires", { date: formatDate(grant.ends_at, i18n.language) }), grant.resets_total > 1 && t("featureA.resetsLeft", { left: grant.resets_left, total: grant.resets_total }), grant.clears?.length && t("featureA.clears", { windows: grant.clears.map((name) => t(`featureA.resetWindows.${name}`, { defaultValue: name.replace(/_/g, " ")})).join(", ")}), grant.paused ? t("featureA.paused") : grant.use_requires_limit && !grant.usable_now ? t("featureA.usableAtLimit") : ""].filter(Boolean).join(" · ")}</span>
                </div>
                {renderAction(key, grantUsable(data, grant), "cedar_ember", grant.id)}
              </div>
            );
          })}
          <div className={cn("claude-limit-reset-row", !session?.available && "claude-limit-reset-row-muted")}>
            <div className="claude-limit-reset-copy">
              <strong>{t("featureA.fiveHourReset")}</strong>
              <span>
                {session?.available
                  ? t("featureA.refillsSession") + (session.resets_per_week ? ` · ${t("featureA.perWeek", { count: session.resets_per_week })}` : "")
                  : session?.next_available_at
                    ? t("featureA.nextAvailable", { date: formatDate(session.next_available_at, i18n.language, true) })
                    : t("featureA.noSessionResets")}
              </span>
            </div>
            {session?.available ? renderAction(sessionKey, session.eligible, "juniper_tide") : null}
          </div>
          {data.cooldown_until ? (
            <p className="quota-account-modal-note">{t("featureA.nextResetAfter", { date: formatDate(data.cooldown_until, i18n.language, true) })}</p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
