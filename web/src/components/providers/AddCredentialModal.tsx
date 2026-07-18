import { useEffect, useState } from "react";
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
          label: label || (isCookie ? "Web cookie" : undefined),
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
      setError(cause instanceof Error ? cause.message : "Failed to save credential");
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
      subtitle={method?.description || "Secrets are encrypted at rest and never returned by the API."}
      icon={isCookie ? "cookie" : "key"}
      size="md"
    >
      <form onSubmit={handleSave} style={{ display: "grid", gap: 12 }}>
        {method?.authHint || isCookie ? (
          <p className="provider-connection-notice">{method?.authHint || method?.description}</p>
        ) : null}
        <div className="inline-fields">
          <Field label="Credential ID" hint="Optional — auto-generated if blank">
            <Input placeholder="auto" value={id} onChange={(e) => setId(e.target.value)} />
          </Field>
          <Field label="Label">
            <Input placeholder="e.g. production key" value={label} onChange={(e) => setLabel(e.target.value)} />
          </Field>
        </div>
        {isOllamaLocal ? (
          <Field label="Ollama host URL" hint="e.g. http://127.0.0.1:11434" required>
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
            <Field label="Auth type">
              <Select value={authType} onChange={(e) => setAuthType(e.target.value as CredentialAuthType)}>
                <option value="api_key">API key</option>
                <option value="service_account">Service account</option>
                <option value="none">None</option>
              </Select>
            </Field>
            <Field label="Email" hint="Optional, for OAuth accounts">
              <Input type="email" placeholder="account@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
            </Field>
          </div>
        ) : null}
        {needsSecret && (
          <Field
            label={isCookie ? "Cookie value" : authType === "service_account" ? "Service account JSON" : isOllamaLocal ? "API key (optional)" : "API key"}
            hint={
              isCookie
                ? method?.authHint
                : authType === "service_account"
                  ? "Service account JSON or key"
                  : method?.apiKeyUrl
                    ? `Get a key from the provider console.`
                    : "API key value"
            }
            required={!isOllamaLocal}
          >
            {isCookie ? (
              <textarea
                className="input"
                rows={4}
                placeholder="Paste session cookie value"
                value={secretValue}
                onChange={(e) => setSecretValue(e.target.value)}
                required
              />
            ) : (
              <Input
                type="password"
                placeholder={
                  authType === "service_account"
                    ? "Paste service account credentials"
                    : isOllamaLocal
                      ? "Optional API key"
                      : "Paste API key"
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
            Get API key <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
          </a>
        ) : null}
        <div className="inline-fields">
          <Field label="Priority" hint="Lower runs first">
            <Input type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
          </Field>
          <Field label="Weight" hint="Relative load share">
            <Input type="number" min={1} value={weight} onChange={(e) => setWeight(Number(e.target.value))} />
          </Field>
        </div>
        <Toggle label="Enabled" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        {error && (
          <p style={{ margin: 0, fontSize: 13, color: "var(--color-danger)", display: "flex", alignItems: "center", gap: 6 }}>
            <span className="material-symbols-outlined" style={{ fontSize: 16 }}>error</span>
            {error}
          </p>
        )}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 4 }}>
          <Button type="button" variant="secondary" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" icon="save" loading={saving}>
            Save credential
          </Button>
        </div>
      </form>
    </Modal>
  );
}
