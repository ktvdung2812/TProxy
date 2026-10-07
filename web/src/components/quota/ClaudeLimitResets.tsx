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

const windowLabels: Record<string, string> = {
  five_hour: "session",
  seven_day: "weekly",
  seven_day_overage_included: "weekly overage",
};

const resultMessages: Record<string, string> = {
  reset: "Limits reset.",
  already_used: "This reset was already used.",
  not_limited: "Nothing to reset — the account is not at a limit.",
  cooldown: "A reset was just used on this account; try again shortly.",
  ineligible: "This account is not eligible for this reset.",
  unavailable: "This reset isn't available right now.",
};

function formatDate(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function formatDateTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

function grantUsable(data: ClaudeLimitResetsData, grant: ClaudeResetGrant) {
  return data.eligible && grant.usable_now && !grant.paused && grant.resets_left > 0;
}

function grantMeta(grant: ClaudeResetGrant) {
  const parts: string[] = [];
  if (grant.ends_at) parts.push(`Expires ${formatDate(grant.ends_at)}`);
  if (grant.resets_total > 1) parts.push(`${grant.resets_left} of ${grant.resets_total} left`);
  if (grant.clears?.length) parts.push(`Clears ${grant.clears.map((name) => windowLabels[name] ?? name.replace(/_/g, " ")).join(", ")}`);
  if (grant.paused) parts.push("Paused");
  else if (grant.use_requires_limit && !grant.usable_now) parts.push("Usable once a limit is hit");
  return parts.join(" · ");
}

export function ClaudeLimitResets({ secret, credentialId, active, onReset }: Props) {
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
      setError(cause instanceof Error ? cause.message : "Failed to load limit resets");
    } finally {
      setLoading(false);
    }
  }, [secret, credentialId]);

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
        setNotice(resultMessages.reset);
        onReset?.();
      } else {
        setError(resultMessages[result.result] ?? `Reset not applied (${result.result}).`);
      }
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to reset limits");
    } finally {
      setClaimingKey(null);
    }
  };

  const renderAction = (key: string, enabled: boolean, program: ClaudeResetProgram, grantId?: string) => {
    if (confirmKey === key) {
      return (
        <div className="claude-limit-reset-actions">
          <Button variant="secondary" size="sm" onClick={() => setConfirmKey(null)}>Cancel</Button>
          <Button variant="danger" size="sm" onClick={() => void claim(key, program, grantId)}>Confirm reset</Button>
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
        Reset for free
      </Button>
    );
  };

  const session = data?.session;
  const sessionKey = "session";

  return (
    <div className="quota-account-modal-section">
      <div className="quota-account-modal-section-head">
        <div className="quota-account-modal-section-head-main">
          <h5>Limit resets</h5>
          <span className="quota-account-modal-section-meta">Free resets Claude grants this account</span>
        </div>
        <button
          type="button"
          className="quota-tracker-icon-btn"
          disabled={loading}
          onClick={() => void load()}
          aria-label="Refresh limit resets"
          title="Refresh limit resets"
        >
          <span className={cn("material-symbols-outlined", loading && "animate-spin")}>refresh</span>
        </button>
      </div>
      {error ? <p className="quota-account-modal-note quota-account-modal-note-error">{error}</p> : null}
      {notice ? <p className="quota-account-modal-note">{notice}</p> : null}
      {loading && !data ? (
        <p className="quota-account-modal-empty">Loading limit resets…</p>
      ) : data ? (
        <div className="claude-limit-reset-list">
          {!data.eligible && data.ineligible_reason ? (
            <p className="quota-account-modal-note">Not eligible for full resets ({data.ineligible_reason.replace(/_/g, " ")}).</p>
          ) : null}
          {data.grants.map((grant) => {
            const key = `grant:${grant.id}`;
            return (
              <div key={grant.id} className="claude-limit-reset-row">
                <div className="claude-limit-reset-copy">
                  <strong title={grant.label || grant.id}>Full reset</strong>
                  <span>{grantMeta(grant)}</span>
                </div>
                {renderAction(key, grantUsable(data, grant), "cedar_ember", grant.id)}
              </div>
            );
          })}
          <div className={cn("claude-limit-reset-row", !session?.available && "claude-limit-reset-row-muted")}>
            <div className="claude-limit-reset-copy">
              <strong>5-hour reset</strong>
              <span>
                {session?.available
                  ? `Refills the session limit${session.resets_per_week ? ` · ${session.resets_per_week} per week` : ""}`
                  : session?.next_available_at
                    ? `Next available ${formatDateTime(session.next_available_at)}`
                    : "None right now. When you get one, it shows up here."}
              </span>
            </div>
            {session?.available ? renderAction(sessionKey, session.eligible, "juniper_tide") : null}
          </div>
          {data.cooldown_until ? (
            <p className="quota-account-modal-note">Next reset possible after {formatDateTime(data.cooldown_until)}.</p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
