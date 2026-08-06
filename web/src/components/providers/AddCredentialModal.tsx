import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Select, Toggle } from "../ui";
import { getProviderTypeInfo } from "./catalog";
import type { ConnectionMethod } from "./connectionMethods";
import { saveCredential } from "./api";

type CredentialAuthType = "api_key" | "service_account" | "none";

type Props = {
  open: boolean;
  providerId: string;
  providerType: string;
  secret: string;
  method?: ConnectionMethod | null;
  onClose: () => void;
  onSaved?: () => void;
};

/**
 * Add a credential (API key / service account / none / cookie) to a provider.
 * Ported in spirit from 9router AddApiKeyModal.
 */
export function AddCredentialModal({ open, providerId, providerType, secret, method, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const info = getProviderTypeInfo(providerType);
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [email, setEmail] = useState("");
  const [authType, setAuthType] = useState<CredentialAuthType>(
    info.defaultAuthType === "none"
      ? "none"
      : info.defaultAuthType === "service_account"
        ? "service_account"
        : "api_key",
  );
  const [secretValue, setSecretValue] = useState("");
  const [hostUrl, setHostUrl] = useState("");
  const [priority, setPriority] = useState(0);
  const [weight, setWeight] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const isCookie = method?.kind === "cookie";
  const isOllamaLocal = providerId === "ollama-local";
  const lockAuthType = method?.kind === "api_key" || method?.kind === "cookie" || method?.kind === "service_account" || method?.kind === "none";

  useEffect(() => {
    if (!open) return;
    setId("");
    setLabel("");
    setEmail("");
    setSecretValue("");
    setHostUrl("");
    setPriority(0);
    setWeight(1);
    setEnabled(true);
    setError("");

    if (method?.kind === "cookie") {
      setAuthType("api_key");
      return;
    }
    if (method?.kind === "service_account") {
      setAuthType("service_account");
      return;
    }
    if (method?.kind === "none") {
      setAuthType("none");
      return;
    }
    if (method?.kind === "api_key") {
      setAuthType("api_key");
      return;
    }

    setAuthType(
      info.defaultAuthType === "none"
        ? "none"
        : info.defaultAuthType === "service_account"
          ? "service_account"
          : "api_key",
    );
  }, [open, providerId, info.defaultAuthType, method]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const resolvedSecret = isOllamaLocal ? hostUrl.trim() || secretValue.trim() || undefined : secretValue || undefined;
      await saveCredential(secret, {
        provider_id: providerId,
        credential: {
          id: id || `${providerId}-cred-${Date.now().toString(36)}`,
          label: label || (isCookie ? t("addCredentialWebCookie") : undefined),
          email: email || undefined,
          auth_type: authType,
          secret: resolvedSecret,
          priority,
          weight,
          enabled,
        },
      });
      onSaved?.();
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("providers.credentialSaveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const needsSecret = authType === "api_key" || authType === "service_account";
  const title = method?.label || `Add credential to ${info.name}`;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      subtitle={method?.description || t("providers.addCredentialSubtitle")}
      icon={isCookie ? "cookie" : "key"}
      size="md"
    >
      <form onSubmit={handleSave} style={{ display: "grid", gap: 12 }}>
        {method?.authHint || isCookie ? (
          <p className="provider-connection-notice">{method?.authHint || method?.description}</p>
        ) : null}
        <div className="inline-fields">
          <Field label={t("providers.credentialId")} hint={t("providers.credentialIdHint")}>
            <Input placeholder={t("providers.credentialIdPlaceholder")} value={id} onChange={(e) => setId(e.target.value)} />
          </Field>
          <Field label={t("providers.label")}>
            <Input placeholder={t("providers.labelPlaceholder")} value={label} onChange={(e) => setLabel(e.target.value)} />
          </Field>
        </div>
        {isOllamaLocal ? (
          <Field label={t("providers.ollamaHostUrl")} hint={t("providers.ollamaHostUrlHint")} required>
            <Input
              placeholder="http://127.0.0.1:11434"
              value={hostUrl}
              onChange={(e) => setHostUrl(e.target.value)}
              required
            />
          </Field>
        ) : null}
        {!lockAuthType ? (
          <div className="inline-fields">
            <Field label={t("providers.authType")}>
              <Select value={authType} onChange={(e) => setAuthType(e.target.value as CredentialAuthType)}>
                <option value="api_key">{t("providers.authTypeApiKey")}</option>
                <option value="service_account">{t("providers.authTypeServiceAccount")}</option>
                <option value="none">{t("providers.authTypeNone")}</option>
              </Select>
            </Field>
            <Field label={t("providers.email")} hint={t("providers.emailHint")}>
              <Input type="email" placeholder={t("providers.emailPlaceholder")} value={email} onChange={(e) => setEmail(e.target.value)} />
            </Field>
          </div>
        ) : null}
        {needsSecret && (
          <Field
            label={isCookie ? t("providers.cookieValue") : authType === "service_account" ? t("providers.serviceAccountJson") : isOllamaLocal ? t("providers.apiKeyOptional") : t("providers.apiKey")}
            hint={
              isCookie
                ? method?.authHint
                : authType === "service_account"
                  ? t("providers.serviceAccountHint")
                  : method?.apiKeyUrl
                    ? t("providers.apiKeyHint")
                    : t("providers.apiKeyValueHint")
            }
            required={!isOllamaLocal}
          >
            {isCookie ? (
              <textarea
                className="input"
                rows={4}
                placeholder={t("providers.cookiePlaceholder")}
                value={secretValue}
                onChange={(e) => setSecretValue(e.target.value)}
                required
              />
            ) : (
              <Input
                type="password"
                placeholder={
                  authType === "service_account"
                    ? t("providers.serviceAccountPlaceholder")
                    : isOllamaLocal
                      ? t("providers.apiKeyOptionalPlaceholder")
                      : t("providers.apiKeyPlaceholder")
                }
                value={secretValue}
                onChange={(e) => setSecretValue(e.target.value)}
                required={!isOllamaLocal}
              />
            )}
          </Field>
        )}
        {method?.apiKeyUrl ? (
          <a className="detail-link" href={method.apiKeyUrl} target="_blank" rel="noreferrer">
            {t("providers.getApiKey")} <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
          </a>
        ) : null}
        <div className="inline-fields">
          <Field label={t("providers.priority")} hint={t("providers.priorityHint")}>
            <Input type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
          </Field>
          <Field label={t("providers.weight")} hint={t("providers.weightHint")}>
            <Input type="number" min={1} value={weight} onChange={(e) => setWeight(Number(e.target.value))} />
          </Field>
        </div>
        <Toggle label={t("providers.enabled")} checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        {error && (
          <p style={{ margin: 0, fontSize: 13, color: "var(--color-danger)", display: "flex", alignItems: "center", gap: 6 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16 }}>error</span>
            {error}
          </p>
        )}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 4 }}>
          <Button type="button" variant="secondary" onClick={onClose} disabled={saving}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" variant="primary" icon="save" loading={saving}>
            {t("providers.saveCredential")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
