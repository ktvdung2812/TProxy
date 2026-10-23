import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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
        setErrorMsg(result.error || t("providers.kiroAutoDetectFailed"));
      }
    } catch (error) {
      setErrorMsg(error instanceof Error ? error.message : t("providers.autoDetectFailed"));
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
      setErrorMsg(t("providers.enterRefreshToken"));
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
      const message = error instanceof Error ? error.message : t("providers.importFailed");
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleImportCLIProxy = async () => {
    if (!cliProxyJson.trim()) {
      setErrorMsg(t("providers.pasteCLIProxyAPI"));
      return;
    }
    setBusy(true);
    setErrorMsg("");
    try {
      await importKiroCLIProxyJSON(secret, { provider_id: providerId, json: cliProxyJson.trim() });
      onComplete?.();
      onClose();
    } catch (error) {
      const message = error instanceof Error ? error.message : t("providers.kiroAuthCliProxyImportFailed");
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleApiKeyImport = async () => {
    if (!apiKey.trim()) {
      setErrorMsg(t("providers.kiroAuthApiKeyRequired"));
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
      const message = error instanceof Error ? error.message : t("providers.kiroAuthApiKeyImportFailed");
      setErrorMsg(message);
      onError?.(message);
    } finally {
      setBusy(false);
    }
  };

  const handleIdcContinue = () => {
    if (!idcStartUrl.trim()) {
      setErrorMsg(t("providers.kiroAuthIdcUrlRequired"));
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
          {t("common.back")}
        </Button>
        <Button variant="primary" size="sm" onClick={handleIdcContinue}>
          {t("providers.kiroAuthContinue")}
        </Button>
      </>
    ) : method === "import" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy || autoDetecting}>
          {t("common.back")}
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="upload"
          loading={busy}
          disabled={autoDetecting || !refreshToken.trim()}
          onClick={() => void handleImportToken()}
        >
          {t("providers.kiroAuthImportToken")}
        </Button>
      </>
    ) : method === "import-cli-proxy" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy}>
          {t("common.back")}
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="upload"
          loading={busy}
          disabled={!cliProxyJson.trim()}
          onClick={() => void handleImportCLIProxy()}
        >
          {t("providers.kiroAuthImportJson")}
        </Button>
      </>
    ) : method === "api-key" ? (
      <>
        <Button variant="ghost" size="sm" onClick={() => setMethod(null)} disabled={busy}>
          {t("common.back")}
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="key"
          loading={busy}
          disabled={!apiKey.trim()}
          onClick={() => void handleApiKeyImport()}
        >
          {t("providers.kiroAuthSaveApiKey")}
        </Button>
      </>
    ) : (
      <Button variant="ghost" size="sm" onClick={onClose}>
        {t("common.cancel")}
      </Button>
    );

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("providers.kiroAuthTitle")}
      subtitle={t("providers.kiroAuthSubtitle")}
      icon="shield"
      size="lg"
      footer={footer}
    >
      {!method ? (
        <div className="kiro-auth-methods">
          <p className="oauth-step-hint">{t("providers.kiroAuthChooseMethod")}</p>
          <button type="button" className="kiro-auth-method" onClick={() => onDeviceFlow({ authMethod: "builder-id" })}>
            <span className="material-symbols-outlined">shield</span>
            <span>
              <strong>{t("providers.kiroAuthBuilderId")}</strong>
              <small>{t("providers.kiroAuthBuilderIdDesc")}</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("idc")}>
            <span className="material-symbols-outlined">business</span>
            <span>
              <strong>{t("providers.kiroAuthIdc")}</strong>
              <small>{t("providers.kiroAuthIdcDesc")}</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("api-key")}>
            <span className="material-symbols-outlined">key</span>
            <span>
              <strong>{t("providers.kiroAuthApiKey")}</strong>
              <small>{t("providers.kiroAuthApiKeyDesc")}</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("import")}>
            <span className="material-symbols-outlined">file_upload</span>
            <span>
              <strong>{t("providers.kiroAuthImportTokenHeader")}</strong>
              <small>{t("providers.kiroAuthImportTokenDesc")}</small>
            </span>
          </button>
          <button type="button" className="kiro-auth-method" onClick={() => setMethod("import-cli-proxy")}>
            <span className="material-symbols-outlined">data_object</span>
            <span>
              <strong>{t("providers.kiroAuthImportCliProxy")}</strong>
              <small>{t("providers.kiroAuthImportCliProxyDesc")}</small>
            </span>
          </button>
        </div>
      ) : null}

      {method === "idc" ? (
        <div className="oauth-connect">
          <Field label={t("providers.kiroAuthIdcStartUrl")} hint={t("providers.kiroAuthIdcStartUrlHint")}>
            <Input
              value={idcStartUrl}
              onChange={(e) => setIdcStartUrl(e.target.value)}
              placeholder="https://your-org.awsapps.com/start"
            />
          </Field>
          <Field label={t("providers.kiroAuthAwsRegion")} hint={t("providers.kiroAuthAwsRegionHint")}>
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
              <span>{t("providers.kiroAuthAutoDetecting")}</span>
            </div>
          ) : null}
          {autoDetected ? (
            <div className="cli-tool-note cli-tool-note-info">
              <span className="material-symbols-outlined">check_circle</span>
              <p>{t("providers.kiroAuthTokenDetected")}</p>
            </div>
          ) : null}
          <Field label={t("providers.kiroAuthRefreshToken")}>
            <Input value={refreshToken} onChange={(e) => setRefreshToken(e.target.value)} placeholder="aorAAAAAG..." />
          </Field>
          {!autoDetecting ? (
            <Button variant="outline" size="sm" icon="refresh" onClick={() => void runAutoDetect()}>
              {t("providers.kiroAuthRetryAutoDetect")}
            </Button>
          ) : null}
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}

      {method === "import-cli-proxy" ? (
        <div className="oauth-connect">
          <Field label={t("providers.kiroAuthCliProxyJson")}>
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
          <Field label={t("providers.kiroAuthApiKeyField")}>
            <Input value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder={t("providers.kiroAuth.apiKeyPlaceholder")} />
          </Field>
          <Field label={t("providers.kiroAuthAwsRegion")} hint={t("providers.kiroAuthAwsRegionHint")}>
            <Input value={apiKeyRegion} onChange={(e) => setApiKeyRegion(e.target.value)} placeholder="us-east-1" />
          </Field>
          {errorMsg ? <p className="oauth-inline-error">{errorMsg}</p> : null}
        </div>
      ) : null}
    </Modal>
  );
}
