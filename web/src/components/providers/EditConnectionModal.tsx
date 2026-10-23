import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal, Toggle } from "../ui";
import { StatusPill, ToolButton } from "../page-kit";
import { checkCredentialHealth, saveCredential, type ProxyPoolOption } from "./api";
import type { AdmissionPolicy, Credential } from "./types";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";

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
  const { masked } = useAccountMask();
  const [admission, setAdmission] = useState<AdmissionPolicy>({max_concurrent_requests:0,max_queue_size:16,queue_timeout_ms:5000});
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
      setAdmission(credential.admission ?? {max_concurrent_requests:0,max_queue_size:16,queue_timeout_ms:5000});
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
      setTestMessage(result.ok ? t("providers.editConnectionHealthy", { status: result.status || "healthy" }) : result.last_error || result.error || t("providers.healthCheckFailed"));
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
          metadata: { ...admission },
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
      title={t("providers.editConnectionTitle", { label: maskAccountText(credential.label || credential.email || credential.id, masked) })}
      subtitle={isOAuth ? t("providers.oauthCredentialHint") : t("providers.leaveSecretBlank")}
      icon="tune"
      size="md"
    >
      <form onSubmit={handleSave} className="pv-form">
        <Field label={t("providers.label")}>
          <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("providers.editConnectionFriendlyName")} />
        </Field>
        <div className="inline-fields">
          <Field label={t("providers.priority")} hint={t("providers.priorityHint")}>
            <Input type="number" value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
          </Field>
          <Field label={t("providers.weight")} hint={t("providers.weightHint")}>
            <Input type="number" min={1} value={weight} onChange={(e) => setWeight(Number(e.target.value))} />
          </Field>
        </div>
        <div className="inline-fields">
          <Field label={t("operations.concurrency")} hint={t("operations.unlimitedHint")}>
            <Input aria-label={t("operations.concurrency")} type="number" min={0} max={10000} value={admission.max_concurrent_requests} onChange={e=>setAdmission({...admission,max_concurrent_requests:Number(e.target.value)})}/>
          </Field>
          <Field label={t("operations.queueSize")}>
            <Input aria-label={t("operations.queueSize")} type="number" min={0} max={1000} value={admission.max_queue_size} onChange={e=>setAdmission({...admission,max_queue_size:Number(e.target.value)})}/>
          </Field>
          <Field label={t("operations.queueTimeout")}>
            <Input aria-label={t("operations.queueTimeout")} type="number" min={0} max={120000} value={admission.queue_timeout_ms} onChange={e=>setAdmission({...admission,queue_timeout_ms:Number(e.target.value)})}/>
          </Field>
        </div>
        <Field label={t("operations.expiry")} hint={t("providers.editConnection.expiryHint")}>
          <Input aria-label={t("operations.expiry")} value={admission.account_expires_at ?? ""} onChange={e=>setAdmission({...admission,account_expires_at:e.target.value})}/>
        </Field>
        {isOAuth && (
          <Field label={t("providers.editConnectionAccountEmail")}>
            <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t("providers.emailPlaceholder")} />
          </Field>
        )}
        {needsSecret && (
          <Field label={t("providers.editConnectionNewSecret")} hint={t("providers.editConnectionNewSecretHint")}>
            <Input
              type="password"
              value={newSecret}
              onChange={(e) => setNewSecret(e.target.value)}
              placeholder={t("providers.editConnectionLeaveBlank")}
            />
          </Field>
        )}
        {proxyPools.length > 0 && (
          <Field label={t("providers.editConnectionProxyPools")} hint={t("providers.editConnectionProxyPoolsHint")}>
            <div className="pv-check-list">
              {proxyPools.map((pool) => (
                <label key={pool.id} className="pv-check-item">
                  <input type="checkbox" className="cp-check" checked={proxyPoolIds.includes(pool.id)} onChange={() => togglePool(pool.id)} />
                  <span>{pool.name || pool.id}</span>
                  <small>{pool.status}</small>
                </label>
              ))}
            </div>
          </Field>
        )}
        <Toggle label={t("providers.enabled")} checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />

        {/* Provider-level health check (tdproxy has no per-credential test) */}
        <div className="pv-form-check">
          <ToolButton icon="monitor_heart" spinning={testing} disabled={testing} onClick={() => void handleTest()}>
            {t("providers.editConnectionRunHealthCheck")}
          </ToolButton>
          {testResult ? (
            <StatusPill tone={testResult === "ok" ? "success" : "danger"} title={testMessage}>
              {testMessage}
            </StatusPill>
          ) : null}
        </div>

        {error && (
          <p className="pv-form-error" role="alert">
            <span className="material-symbols-outlined" aria-hidden>error</span>
            {error}
          </p>
        )}
        <div className="pv-form-actions">
          <Button type="button" variant="secondary" onClick={onClose} disabled={saving}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" variant="primary" icon="save" loading={saving}>
            {t("providers.editConnectionSaveChanges")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
