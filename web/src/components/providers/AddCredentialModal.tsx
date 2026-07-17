import { useEffect, useState } from "react";
import { Button, Field, Input, Modal, Select, Toggle } from "../ui";
import { getProviderTypeInfo } from "./catalog";
import { saveCredential } from "./api";

type Props = {
  open: boolean;
  providerId: string;
  providerType: string;
  secret: string;
  onClose: () => void;
  onSaved?: () => void;
};

/**
 * Add a credential (API key / service account / none) to a provider.
 * Ported in spirit from 9router AddApiKeyModal — single mode only (no bulk),
 * since tdproxy generates credential ids and does not upsert by name.
 */
export function AddCredentialModal({ open, providerId, providerType, secret, onClose, onSaved }: Props) {
  const info = getProviderTypeInfo(providerType);
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [email, setEmail] = useState("");
  const [authType, setAuthType] = useState<"api_key" | "service_account" | "none">(info.defaultAuthType === "none" ? "none" : info.defaultAuthType === "service_account" ? "service_account" : "api_key");
  const [secretValue, setSecretValue] = useState("");
  const [priority, setPriority] = useState(0);
  const [weight, setWeight] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  // Reset form whenever the modal opens or the provider changes.
  useEffect(() => {
    if (open) {
      setId("");
      setLabel("");
      setEmail("");
      setSecretValue("");
      setPriority(0);
      setWeight(1);
      setEnabled(true);
      setError("");
      setAuthType(info.defaultAuthType === "none" ? "none" : info.defaultAuthType === "service_account" ? "service_account" : "api_key");
    }
  }, [open, providerId, info.defaultAuthType]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      await saveCredential(secret, {
        provider_id: providerId,
        credential: {
          id: id || `${providerId}-cred-${Date.now().toString(36)}`,
          label: label || undefined,
          email: email || undefined,
          auth_type: authType,
          secret: secretValue || undefined,
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

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`Add credential to ${info.name}`}
      subtitle="Secrets are encrypted at rest and never returned by the API."
      icon="key"
      size="md"
    >
      <form onSubmit={handleSave} style={{ display: "grid", gap: 12 }}>
        <div className="inline-fields">
          <Field label="Credential ID" hint="Optional — auto-generated if blank">
            <Input placeholder="auto" value={id} onChange={(e) => setId(e.target.value)} />
          </Field>
          <Field label="Label">
            <Input placeholder="e.g. production key" value={label} onChange={(e) => setLabel(e.target.value)} />
          </Field>
        </div>
        <div className="inline-fields">
          <Field label="Auth type">
            <Select value={authType} onChange={(e) => setAuthType(e.target.value as typeof authType)}>
              <option value="api_key">API key</option>
              <option value="service_account">Service account</option>
              <option value="none">None</option>
            </Select>
          </Field>
          <Field label="Email" hint="Optional, for OAuth accounts">
            <Input type="email" placeholder="account@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
        </div>
        {needsSecret && (
          <Field
            label="Secret"
            hint={authType === "service_account" ? "Service account JSON or key" : "API key value"}
            required
          >
            <Input
              type="password"
              placeholder={authType === "service_account" ? "Paste service account credentials" : "Paste API key"}
              value={secretValue}
              onChange={(e) => setSecretValue(e.target.value)}
              required
            />
          </Field>
        )}
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
