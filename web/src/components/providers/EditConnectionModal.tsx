import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal } from "../ui";
import { checkCredentialHealth, saveCredential, type ProxyPoolOption } from "./api";
import type { Credential } from "./types";

type Props = {
  open: boolean;
  providerId: string;
  credential: Credential | null;
  proxyPools: ProxyPoolOption[];
  secret: string;
  onClose: () => void;
  onSaved?: () => void;
};

/**
 * Edit an existing credential — name/priority/weight/email/secret + proxy pool
 * binding. Ported from 9router EditConnectionModal.js, adapted to tdproxy's
 * PUT /api/admin/credentials endpoint.
 *
 * tdproxy has no per-credential test/validate, so the "Check" action runs the
 * provider-level health check (POST /api/admin/providers/{id}/health), which is
 * the closest equivalent and refreshes credential status as a side effect.
 */
export function EditConnectionModal({ open, providerId, credential, proxyPools, secret, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const [label, setLabel] = useState("");
  const [email, setEmail] = useState("");
  const [priority, setPriority] = useState(0);
  const [weight, setWeight] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [newSecret, setNewSecret] = useState("");
  const [proxyPoolIds, setProxyPoolIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<null | "ok" | "error">(null);
  const [testMessage, setTestMessage] = useState("");
  const [error, setError] = useState("");

  // Hydrate form when the target credential changes.
  useEffect(() => {
    if (open && credential) {
      setLabel(credential.label || "");
      setEmail(credential.email || "");
      setPriority(credential.priority ?? 0);
      setWeight(credential.weight ?? 1);
      setEnabled(credential.enabled);
      setNewSecret("");
      setProxyPoolIds(credential.proxy_pool_ids || []);
      setTestResult(null);
      setTestMessage("");
      setError("");
    }
  }, [open, credential]);

  if (!credential) return null;

  const isOAuth = credential.auth_type === "oauth";
  const needsSecret = credential.auth_type === "api_key" || credential.auth_type === "service_account";

  const handleTest = async () => {
    setTesting(true);
    setTestResult(null);
    setTestMessage("");
    try {
      const result = await checkCredentialHealth(secret, credential.id);
      setTestResult(result.ok ? "ok" : "error");
      setTestMessage(result.ok ? `Connection ${result.status || "healthy"}` : result.last_error || result.error || t("providers.healthCheckFailed"));
    } catch (cause) {
      setTestResult("error");
      setTestMessage(cause instanceof Error ? cause.message : t("providers.healthCheckFailed"));
    } finally {
      setTesting(false);
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      await saveCredential(secret, {
        provider_id: providerId,
        credential: {
          id: credential.id,
          label: label || undefined,
          email: email || undefined,
          auth_type: credential.auth_type as "api_key" | "oauth" | "service_account" | "none",
          secret: newSecret || undefined,
          priority,
          weight,
          enabled,
          proxy_pools: proxyPoolIds,
        },
      });
      onSaved?.();
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("providers.failedSaveCredential"));
    } finally {
      setSaving(false);
    }
  };

  const togglePool = (id: string) => {
    setProxyPoolIds((current) => (current.includes(id) ? current.filter((p) => p !== id) : [...current, id]));
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`Edit ${credential.label || credential.id}`}
      subtitle={isOAuth ? t("providers.oauthCredentialHint") : t("providers.leaveSecretBlank")}
      icon="edit"
      size="md"
    >
      <form onSubmit={handleSave} style={{ display: "grid", gap: 12 }}>
        <Field label="Label">
          <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Friendly name" />
        </Field>
        <div className="inline-fields">
          <Field label="Priority" hint="Lower runs first">
            <Input type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
          </Field>
          <Field label="Weight" hint="Relative load share">
            <Input type="number" min={1} value={weight} onChange={(e) => setWeight(Number(e.target.value))} />
          </Field>
        </div>
        {isOAuth && (
          <Field label="Account email">
            <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="account@example.com" />
          </Field>
        )}
        {needsSecret && (
          <Field
            label="New secret"
            hint="Optional — only set to rotate. Encrypted at rest, never returned."
          >
            <Input
              type="password"
              value={newSecret}
              onChange={(e) => setNewSecret(e.target.value)}
              placeholder="Leave blank to keep current"
            />
          </Field>
        )}
        {proxyPools.length > 0 && (
          <Field label="Proxy pools" hint="Bind this credential to one or more egress pools.">
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {proxyPools.map((pool) => (
                <label key={pool.id} style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13, cursor: "pointer" }}>
                  <input
                    type="checkbox"
                    checked={proxyPoolIds.includes(pool.id)}
                    onChange={() => togglePool(pool.id)}
                  />
                  <span style={{ color: "var(--color-text-main)" }}>{pool.name || pool.id}</span>
                  <span style={{ color: "var(--color-text-muted)" }}>· {pool.status}</span>
                </label>
              ))}
            </div>
          </Field>
        )}
        <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          <span style={{ fontSize: 14, color: "var(--color-text-main)" }}>Enabled</span>
        </label>

        {/* Provider-level health check (tdproxy has no per-credential test) */}
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: 12, background: "var(--color-surface-2)", borderRadius: 10 }}>
          <Button type="button" variant="outline" size="sm" icon="monitor_heart" onClick={handleTest} loading={testing} disabled={testing}>
            Run health check
          </Button>
          {testResult && (
            <span style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 13, color: testResult === "ok" ? "var(--color-success)" : "var(--color-danger)" }}>
              <span className="material-symbols-outlined" style={{ fontSize: 18 }}>
                {testResult === "ok" ? "check_circle" : "error"}
              </span>
              {testMessage}
            </span>
          )}
        </div>

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
            Save changes
          </Button>
        </div>
      </form>
    </Modal>
  );
}
