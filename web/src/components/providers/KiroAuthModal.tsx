import { useCallback, useEffect, useState } from "react";
import { Button, Field, Input, Modal } from "../ui";
import {
  autoImportKiroTokens,
  importKiroAPIKey,
  importKiroCLIProxyJSON,
  importKiroRefreshToken,
} from "./api";

export type KiroDeviceConfig = {
  authMethod: "builder-id" | "idc";
  startUrl?: string;
  region?: string;
};

type Props = {
  open: boolean;
  secret: string;
  providerId: string;
  onClose: () => void;
  onDeviceFlow: (config: KiroDeviceConfig) => void;
  onComplete?: () => void;
  onError?: (message: string) => void;
};

type Method = "idc" | "api-key" | "import" | "import-cli-proxy" | null;

export function KiroAuthModal({
  open,
  secret,
  providerId,
  onClose,
  onDeviceFlow,
  onComplete,
  onError,
}: Props) {
  const [method, setMethod] = useState<Method>(null);
  const [idcStartUrl, setIdcStartUrl] = useState("");
  const [idcRegion, setIdcRegion] = useState("us-east-1");
  const [refreshToken, setRefreshToken] = useState("");
  const [cliProxyJson, setCliProxyJson] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [apiKeyRegion, setApiKeyRegion] = useState("us-east-1");
  const [errorMsg, setErrorMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [autoDetecting, setAutoDetecting] = useState(false);
  const [autoDetected, setAutoDetected] = useState(false);
  const [idcCredentials, setIdcCredentials] = useState<{
    clientId?: string;
    clientSecret?: string;
    region?: string;
    profileArn?: string;
  } | null>(null);

  const reset = useCallback(() => {
    setMethod(null);
    setIdcStartUrl("");
    setIdcRegion("us-east-1");
    setRefreshToken("");
    setCliProxyJson("");
    setApiKey("");
    setApiKeyRegion("us-east-1");
    setErrorMsg("");
    setBusy(false);
    setAutoDetecting(false);
    setAutoDetected(false);
    setIdcCredentials(null);
  }, []);

  useEffect(() => {
    if (!open) return;
    reset();
  }, [open, reset]);

  const runAutoDetect = useCallback(async () => {
    setAutoDetecting(true);
    setErrorMsg("");
    setAutoDetected(false);
    setIdcCredentials(null);
    try {
      const result = await autoImportKiroTokens(secret);
      if (result.found && result.refresh_token) {
        setRefreshToken(result.refresh_token);
        setAutoDetected(true);
        if (result.client_id && result.client_secret) {
          setIdcCredentials({
            clientId: result.client_id,
            clientSecret: result.client_secret,
            region: result.region,
            profileArn: result.profile_arn,
          });
        }
      } else {
        setErrorMsg(result.error || "Could not auto-detect Kiro token");
      }
    } catch (error) {
      setErrorMsg(error instanceof Error ? error.message : "Auto-detect failed");
    } finally {
      setAutoDetecting(false);
    }
  }, [secret]);

  useEffect(() => {
    if (!open || method !== "import") return;
    void runAutoDetect();
  }, [open, method, runAutoDetect]);

  const handleImportToken = async () => {
    if (!refreshToken.trim()) {
      setErrorMsg("Please enter a refresh token");
      return;
    }
    setBusy(true);
    setErrorMsg("");
    try {
      await importKiroRefreshToken(secret, {
        provider_id: providerId,
        refresh_token: refreshToken.trim(),
        client_id: idcCredentials?.clientId,
        client_secret: idcCredentials?.clientSecret,
        region: idcCredentials?.region,
        auth_method: idcCredentials?.clientId ? "idc" : "imported",
        profile_arn: idcCredentials?.profileArn,
      });
      onComplete?.();
      onClose();
    } catch (error) {
      const message = error instanceof Error ? error.message : "Import failed";
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleImportCLIProxy = async () => {
    if (!cliProxyJson.trim()) {
      setErrorMsg("Please paste CLIProxyAPI auth JSON");
      return;
    }
    setBusy(true);
    setErrorMsg("");
    try {
      await importKiroCLIProxyJSON(secret, { provider_id: providerId, json: cliProxyJson.trim() });
      onComplete?.();
      onClose();
    } catch (error) {
      const message = error instanceof Error ? error.message : "CLIProxyAPI import failed";
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleApiKeyImport = async () => {
    if (!apiKey.trim()) {
      setErrorMsg("Please enter an API key");
      return;
    }
    setBusy(true);
    setErrorMsg("");
    try {
      await importKiroAPIKey(secret, {
        provider_id: providerId,
        api_key: apiKey.trim(),
        region: apiKeyRegion.trim() || "us-east-1",
      });
      onComplete?.();
      onClose();
    } catch (error) {
      const message = error instanceof Error ? error.message : "API key import failed";
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleIdcContinue = () => {
    if (!idcStartUrl.trim()) {
      setErrorMsg("Please enter your IDC start URL");
      return;
    }
    onDeviceFlow({
      authMethod: "idc",
      startUrl: idcStartUrl.trim(),
      region: idcRegion.trim() || "us-east-1",
    });
  };

  const footer =
    method === "idc" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)}>
          Back
        </Button>
        <Button variant="primary" size="sm" onClick={handleIdcContinue}>
          Continue
        </Button>
      </>
    ) : method === "import" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy || autoDetecting}>
          Back
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="upload"
          loading={busy}
          disabled={autoDetecting || !refreshToken.trim()}
          onClick={() => void handleImportToken()}
        >
          Import token
        </Button>
      </>
    ) : method === "import-cli-proxy" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy}>
          Back
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="upload"
          loading={busy}
          disabled={!cliProxyJson.trim()}
          onClick={() => void handleImportCLIProxy()}
        >
          Import JSON
        </Button>
      </>
    ) : method === "api-key" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy}>
          Back
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="key"
          loading={busy}
          disabled={!apiKey.trim()}
          onClick={() => void handleApiKeyImport()}
        >
          Save API key
        </Button>
      </>
    ) : (
      <Button variant="ghost" size="sm" onClick={onClose}>
        Cancel
      </Button>
    );

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Connect Kiro"
      subtitle="Choose your authentication method"
      icon="shield"
      size="lg"
      footer={footer}
    >
      {!method ? (
        <div className="kiro-auth-methods">
          <p className="oauth-step-hint">Choose how you want to authenticate with Kiro / CodeWhisperer.</p>
          <button type="button" className="kiro-auth-method" onClick={() => onDeviceFlow({ authMethod: "builder-id" })}>
            <span className="material-symbols-outlined">shield</span>
            <span>
              <strong>AWS Builder ID</strong>
              <small>Recommended for most users. Free AWS account required.</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("idc")}>
            <span className="material-symbols-outlined">business</span>
            <span>
              <strong>AWS IAM Identity Center</strong>
              <small>For enterprise users with custom AWS IAM Identity Center.</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("api-key")}>
            <span className="material-symbols-outlined">key</span>
            <span>
              <strong>API Key</strong>
              <small>Long-lived Kiro / CodeWhisperer API key (headless auth).</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("import")}>
            <span className="material-symbols-outlined">file_upload</span>
            <span>
              <strong>Import Token</strong>
              <small>Paste refresh token from Kiro IDE or auto-detect from AWS SSO cache.</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("import-cli-proxy")}>
            <span className="material-symbols-outlined">data_object</span>
            <span>
              <strong>Import CLIProxyAPI JSON</strong>
              <small>external_idp auth JSON from CLIProxyAPI / Microsoft login.</small>
            </span>
          </button>
        </div>
      ) : null}

      {method === "idc" ? (
        <div className="oauth-connect">
          <Field label="IDC Start URL" hint="Your organization's AWS IAM Identity Center URL">
            <Input
              value={idcStartUrl}
              onChange={(e) => setIdcStartUrl(e.target.value)}
              placeholder="https://your-org.awsapps.com/start"
            />
          </Field>
          <Field label="AWS Region" hint="Default: us-east-1">
            <Input value={idcRegion} onChange={(e) => setIdcRegion(e.target.value)} placeholder="us-east-1" />
          </Field>
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}

      {method === "import" ? (
        <div className="oauth-connect">
          {autoDetecting ? (
            <div className="oauth-status pending">
              <span className="material-symbols-outlined animate-spin">progress_activity</span>
              <span>Auto-detecting token from AWS SSO cache…</span>
            </div>
          ) : null}
          {autoDetected ? (
            <div className="cli-tool-note cli-tool-note-info">
              <span className="material-symbols-outlined">check_circle</span>
              <p>Token auto-detected from Kiro IDE / AWS SSO cache.</p>
            </div>
          ) : null}
          <Field label="Refresh token">
            <Input value={refreshToken} onChange={(e) => setRefreshToken(e.target.value)} placeholder="aorAAAAAG..." />
          </Field>
          {!autoDetecting ? (
            <Button variant="outline" size="sm" icon="refresh" onClick={() => void runAutoDetect()}>
              Retry auto-detect
            </Button>
          ) : null}
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}

      {method === "import-cli-proxy" ? (
        <div className="oauth-connect">
          <Field label="CLIProxyAPI auth JSON">
            <textarea
              className="input kiro-json-input"
              rows={8}
              value={cliProxyJson}
              onChange={(e) => setCliProxyJson(e.target.value)}
              placeholder='{"auth_method":"external_idp",...}'
            />
          </Field>
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}

      {method === "api-key" ? (
        <div className="oauth-connect">
          <Field label="API key">
            <Input value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="Kiro API key" />
          </Field>
          <Field label="AWS Region" hint="Default: us-east-1">
            <Input value={apiKeyRegion} onChange={(e) => setApiKeyRegion(e.target.value)} placeholder="us-east-1" />
          </Field>
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}
    </Modal>
  );
}
