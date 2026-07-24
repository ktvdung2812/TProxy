import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Field, Input, Modal } from "../ui";
import { getProviderTypeInfo, defaultOAuthMode, usesBrowserOAuth, allowsStatelessOAuthCallback, parseClineCallbackUrl } from "./catalog";
import type { KiroDeviceConfig } from "./KiroAuthModal";
import {
  cancelOAuth,
  completeOAuthCallback,
  pollOAuthStatus,
  startOAuth,
  type OAuthSessionStatus,
  type OAuthStartResponse,
} from "./api";

type Phase = "idle" | "starting" | "pending" | "complete" | "failed" | "cancelled";

type Props = {
  open: boolean;
  providerId: string;
  providerType: string;
  presetId?: string;
  secret: string;
  credentialId?: string;
  initialLabel?: string;
  initialEmail?: string;
  autoStart?: boolean;
  kiroConfig?: KiroDeviceConfig;
  onClose: () => void;
  onComplete?: () => void;
  onError?: (message: string) => void;
};

/**
 * OAuth connect modal — browser PKCE for Codex/Claude/Antigravity (9router-style),
 * device authorization for Kimi/xAI/Qwen.
 */
export function OAuthModal({
  open,
  providerId,
  providerType,
  presetId,
  secret,
  credentialId,
  initialLabel,
  initialEmail,
  autoStart,
  kiroConfig,
  onClose,
  onComplete,
  onError,
}: Props) {
  const info = getProviderTypeInfo(providerType);
  const browserFlow = usesBrowserOAuth(providerType, presetId);
  const [phase, setPhase] = useState<Phase>("idle");
  const [label, setLabel] = useState("");
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [session, setSession] = useState<OAuthSessionStatus | null>(null);
  const [startResp, setStartResp] = useState<OAuthStartResponse | null>(null);
  const [errorMsg, setErrorMsg] = useState("");
  const [manualUrl, setManualUrl] = useState("");
  const [copied, setCopied] = useState(false);
  const popupRef = useRef<Window | null>(null);
  const pollRef = useRef<number | null>(null);
  const closeTimerRef = useRef<number | null>(null);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    if (open) {
      setPhase("idle");
      setSessionId(null);
      setSession(null);
      setStartResp(null);
      setErrorMsg("");
      setManualUrl("");
      setCopied(false);
      setLabel(initialLabel || "");
    }
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current);
      pollRef.current = null;
      clearCloseTimer();
    };
  }, [open, initialLabel, clearCloseTimer]);

  useEffect(() => {
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current);
      popupRef.current?.close();
      clearCloseTimer();
    };
  }, [clearCloseTimer]);

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      window.clearInterval(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const handleTerminal = useCallback(
    (status: OAuthSessionStatus) => {
      stopPolling();
      setSession(status);
      if (status.status === "complete") {
        setPhase("complete");
        popupRef.current?.close();
        onComplete?.();
        clearCloseTimer();
        closeTimerRef.current = window.setTimeout(() => {
          onClose();
        }, 3000);
      } else if (status.status === "failed" || status.status === "expired") {
        setPhase("failed");
        setErrorMsg(status.error_code || `OAuth ${status.status}`);
        onError?.(status.error_code || `OAuth ${status.status}`);
      } else if (status.status === "cancelled") {
        setPhase("cancelled");
      }
    },
    [stopPolling, onComplete, onError, onClose, clearCloseTimer],
  );

  const startPolling = useCallback(
    (id: string) => {
      stopPolling();
      pollRef.current = window.setInterval(async () => {
        try {
          const status = await pollOAuthStatus(secret, id);
          setSession(status);
          if (["complete", "failed", "cancelled", "expired"].includes(status.status)) {
            handleTerminal(status);
          }
        } catch {
          /* keep polling on transient errors */
        }
      }, 1500);
    },
    [secret, stopPolling, handleTerminal],
  );

  const openAuthWindow = useCallback((url: string) => {
    popupRef.current = window.open(url, "tproxy-oauth", "width=600,height=720,noopener,noreferrer");
    if (!popupRef.current) {
      window.open(url, "_blank", "noopener,noreferrer");
    }
  }, []);

  const handleStart = useCallback(async () => {
    setPhase("starting");
    setErrorMsg("");
    try {
      const started = await startOAuth(secret, {
        provider_id: providerId,
        credential_id: credentialId,
        label: label || initialLabel || undefined,
        email: initialEmail,
        mode: defaultOAuthMode(providerType, presetId),
        kiro_region: kiroConfig?.region,
        kiro_start_url: kiroConfig?.startUrl,
        kiro_auth_method: kiroConfig?.authMethod,
      });
      setSessionId(started.session_id);
      setStartResp(started);
      setSession({
        session_id: started.session_id,
        provider_id: started.provider_id,
        credential_id: started.credential_id,
        mode: started.mode,
        status: "pending",
        expires_at: started.expires_at,
      });
      setPhase("pending");

      if (started.mode === "browser" && started.authorization_url) {
        openAuthWindow(started.authorization_url);
      } else if (started.mode === "device" && started.verification_uri) {
        window.open(started.verification_uri, "_blank", "noopener,noreferrer");
      }

      startPolling(started.session_id);
    } catch (cause) {
      setPhase("failed");
      const msg = cause instanceof Error ? cause.message : "Failed to start OAuth";
      setErrorMsg(msg);
      onError?.(msg);
    }
  }, [
    secret,
    providerId,
    providerType,
    presetId,
    label,
    initialLabel,
    initialEmail,
    credentialId,
    kiroConfig,
    startPolling,
    openAuthWindow,
    onError,
  ]);

  useEffect(() => {
    if (open && autoStart && phase === "idle") {
      void handleStart();
    }
  }, [open, autoStart, phase, handleStart]);

  const handleCancel = useCallback(async () => {
    stopPolling();
    clearCloseTimer();
    popupRef.current?.close();
    if (sessionId) {
      try {
        await cancelOAuth(secret, sessionId);
      } catch {
        /* ignore */
      }
    }
    onClose();
  }, [sessionId, secret, stopPolling, onClose, clearCloseTimer]);

  const handleCopyAuthUrl = useCallback(async () => {
    const url = startResp?.authorization_url;
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setErrorMsg("Could not copy authorization URL");
    }
  }, [startResp?.authorization_url]);

  const handleManualCallback = useCallback(async () => {
    if (!sessionId || !manualUrl.trim()) return;
    setErrorMsg("");
    try {
      const parsed = new URL(manualUrl.trim());
      const code =
        (providerType === "cline" || providerType === "clinepass"
          ? parseClineCallbackUrl(manualUrl.trim())
          : null) ||
        parsed.searchParams.get("code") ||
        parsed.searchParams.get("token");
      const state = parsed.searchParams.get("state") || "";
      const oauthError = parsed.searchParams.get("error");
      const statelessCallback = allowsStatelessOAuthCallback(providerType);
      if (oauthError) {
        throw new Error(parsed.searchParams.get("error_description") || oauthError);
      }
      if (!code) {
        throw new Error("Callback URL must include a code (or token) query parameter");
      }
      if (!state && !statelessCallback) {
        throw new Error("Callback URL must include code and state query parameters");
      }
      const status = await completeOAuthCallback(secret, code, state || undefined);
      setSession(status);
      if (["complete", "failed", "cancelled", "expired"].includes(status.status)) {
        handleTerminal(status);
      } else {
        startPolling(sessionId);
      }
    } catch (cause) {
      const msg = cause instanceof Error ? cause.message : "Failed to submit callback URL";
      setErrorMsg(msg);
      onError?.(msg);
    }
  }, [sessionId, manualUrl, secret, providerType, handleTerminal, startPolling, onError]);

  const authUrl = startResp?.authorization_url || "";
  const isPending = phase === "pending";
  const isStarting = phase === "starting";
  const showBrowserConnect = browserFlow && (isStarting || isPending);
  const showDeviceConnect = !browserFlow && (isStarting || isPending);

  return (
    <Modal
      open={open}
      onClose={handleCancel}
      title={`Connect ${info.name}`}
      subtitle="Sign in with your provider account to link a credential."
      icon="lock_person"
      size="lg"
      footer={
        phase === "idle" ? (
          <>
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" icon="login" disabled={!info.supportsOAuth} onClick={handleStart}>
              Sign in
            </Button>
          </>
        ) : phase === "complete" ? (
          <Button variant="primary" icon="check" onClick={onClose}>
            Done
          </Button>
        ) : showBrowserConnect ? (
          <>
            <Button variant="secondary" onClick={handleCancel}>
              Cancel
            </Button>
            <Button variant="outline" icon="open_in_new" onClick={() => authUrl && openAuthWindow(authUrl)} disabled={!authUrl}>
              Open login page
            </Button>
            <Button variant="primary" icon="link" onClick={() => void handleManualCallback()} disabled={!manualUrl.trim()}>
              Submit callback URL
            </Button>
          </>
        ) : (
          <Button variant="danger" icon="close" onClick={handleCancel} disabled={isStarting}>
            Cancel authorization
          </Button>
        )
      }
    >
      {phase === "idle" && !autoStart && (
        <Field label="Account label" hint="Optional friendly name for this credential.">
          <Input placeholder="e.g. primary" value={label} onChange={(e) => setLabel(e.target.value)} />
        </Field>
      )}

      {showBrowserConnect && (
        <div className="oauth-connect">
          <div className="oauth-status pending">
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
            <span>{isStarting ? "Starting authorization…" : "Waiting for popup authorization…"}</span>
          </div>

          <div className="oauth-divider">
            <span>Or paste callback URL manually</span>
          </div>

          <div className="oauth-step">
            <p className="oauth-step-label">Step 1: Open this URL in your browser</p>
            <div className="oauth-url-row">
              <Input value={authUrl} readOnly className="oauth-url-input" />
              <Button variant="secondary" size="sm" icon={copied ? "check" : "content_copy"} onClick={() => void handleCopyAuthUrl()} disabled={!authUrl}>
                {copied ? "Copied" : "Copy"}
              </Button>
              <Button variant="secondary" size="sm" icon="open_in_new" onClick={() => authUrl && openAuthWindow(authUrl)} disabled={!authUrl}>
                Open
              </Button>
            </div>
          </div>

          <div className="oauth-step">
            <p className="oauth-step-label">Step 2: Paste the callback URL here</p>
            <p className="oauth-step-hint">
              {providerType === "cline" || providerType === "clinepass"
                ? "After sign-in, paste the full URL from authkit.cline.bot (for example .../device?code=...)."
                : "After authorization, copy the full URL from your browser address bar."}
            </p>
            <Input
              placeholder={
                providerType === "cline" || providerType === "clinepass"
                  ? "https://authkit.cline.bot/device?user_code=...&code=..."
                  : allowsStatelessOAuthCallback(providerType)
                    ? "http://localhost:1455/auth/callback?code=..."
                    : "http://localhost:1455/auth/callback?code=...&state=..."
              }
              value={manualUrl}
              onChange={(e) => setManualUrl(e.target.value)}
              className="oauth-url-input"
            />
          </div>
        </div>
      )}

      {showDeviceConnect && (
        <div className="oauth-connect">
          <div className="oauth-status pending">
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
            <span>{isStarting ? "Starting authorization…" : "Waiting for authorization to complete…"}</span>
          </div>
          <p className="oauth-step-hint">
            Complete sign-in in the browser tab that opened. This provider uses device authorization — no code entry is required in the dashboard.
          </p>
          {startResp?.verification_uri && (
            <Button variant="outline" size="sm" icon="open_in_new" onClick={() => window.open(startResp.verification_uri, "_blank", "noopener,noreferrer")}>
              Reopen sign-in page
            </Button>
          )}
        </div>
      )}

      {phase === "complete" && (
        <div className="oauth-status success">
          <span className="material-symbols-outlined">check_circle</span>
          <span>Your {info.name} account has been connected. Closing in 3 seconds…</span>
        </div>
      )}

      {phase === "failed" && (
        <div className="oauth-status error">
          <span className="material-symbols-outlined">error</span>
          <span>Connection failed: {errorMsg}</span>
        </div>
      )}

      {errorMsg && isPending && (
        <div className="oauth-status error oauth-inline-error">
          <span className="material-symbols-outlined">error</span>
          <span>{errorMsg}</span>
        </div>
      )}
    </Modal>
  );
}
