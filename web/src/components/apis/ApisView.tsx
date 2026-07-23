import { useCallback, useEffect, useMemo, useState } from "react";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { useApiKeySecrets } from "../../hooks/useApiKeySecrets";
import { defaultApiKey, isLocalDashboardHost } from "../../devDefaults";
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  Field,
  Input,
  Modal,
  Select,
  Toggle,
} from "../ui";
import { createApiKey, deleteApiKey, fetchApiKeyUsage, toggleApiKey, updateApiKey } from "./api";
import { getStoredApiKeySecret, maskApiKeySecret, storeApiKeySecret } from "../../lib/apiKeySecrets";
import { ApiKeySelect } from "../cli-tools/ApiKeySelect";
import { TunnelSection } from "./TunnelSection";
import { EndpointRow } from "./EndpointRow";
import { SecurityWarning } from "./SecurityWarning";
import type { ApiKeyFormData, ApiKeyRecord, ApiKeyUsage } from "./types";
import {
  PROXY_ENDPOINTS,
  apiKeyToForm,
  buildCurlExample,
  emptyApiKeyForm,
  formatLimitSummary,
  gatewayBaseUrl,
} from "./utils";

type Props = {
  secret: string;
  apiKeys: ApiKeyRecord[];
  modelOptions: Array<{ value: string; label: string }>;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
  onMutated?: () => void;
};

function usd(value: number) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 4,
  }).format(value || 0);
}

function compact(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value || 0);
}

export function ApisView({ secret, apiKeys, modelOptions, onError, onNotice, onMutated }: Props) {
  useApiKeySecrets();
  const [usageById, setUsageById] = useState<Record<string, ApiKeyUsage>>({});
  const [loadingUsage, setLoadingUsage] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showAdvancedCreate, setShowAdvancedCreate] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [editingKey, setEditingKey] = useState<ApiKeyRecord | null>(null);
  const [formData, setFormData] = useState<ApiKeyFormData>(emptyApiKeyForm());
  const [editSecret, setEditSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [createdSecret, setCreatedSecret] = useState<string | null>(null);
  const [confirmState, setConfirmState] = useState<{
    title: string;
    message: string;
    confirmText?: string;
    onConfirm: () => void;
  } | null>(null);
  const [exampleApiKey, setExampleApiKey] = useState("");
  const [exampleModel, setExampleModel] = useState(() => modelOptions[0]?.value ?? "");
  const [showRoutes, setShowRoutes] = useState(false);
  const [isRemoteHost, setIsRemoteHost] = useState(false);
  const [baseUrl, setBaseUrl] = useState("/v1");
  const { copied, copy } = useCopyToClipboard();

  useEffect(() => {
    if (typeof window === "undefined") return;
    setBaseUrl(gatewayBaseUrl());
    setIsRemoteHost(!["localhost", "127.0.0.1", "::1"].includes(window.location.hostname));
    if (isLocalDashboardHost() && !getStoredApiKeySecret("local")) {
      storeApiKeySecret("local", defaultApiKey());
    }
  }, []);

  const loadUsage = useCallback(async () => {
    setLoadingUsage(true);
    try {
      const result = await fetchApiKeyUsage(secret);
      const mapped: Record<string, ApiKeyUsage> = {};
      for (const item of result.api_keys || []) {
        mapped[item.id] = item;
      }
      setUsageById(mapped);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load API key usage");
    } finally {
      setLoadingUsage(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void loadUsage();
  }, [loadUsage, apiKeys]);

  useEffect(() => {
    if (!exampleModel && modelOptions[0]?.value) {
      setExampleModel(modelOptions[0].value);
    }
  }, [exampleModel, modelOptions]);

  const curlExample = useMemo(
    () => buildCurlExample(baseUrl, exampleApiKey, exampleModel),
    [baseUrl, exampleApiKey, exampleModel],
  );

  const resetCreateForm = () => {
    setFormData(emptyApiKeyForm());
    setShowAdvancedCreate(false);
  };

  const openCreateModal = () => {
    resetCreateForm();
    setShowCreateModal(true);
  };

  const openEditModal = (key: ApiKeyRecord) => {
    setEditingKey(key);
    setFormData(apiKeyToForm(key));
    setEditSecret(getStoredApiKeySecret(key.id) || "");
    setShowEditModal(true);
  };

  const handleCreate = async () => {
    if (!formData.name.trim()) return;
    setSaving(true);
    try {
      const result = await createApiKey(secret, formData);
      storeApiKeySecret(result.id, result.key);
      setCreatedSecret(result.key);
      setShowCreateModal(false);
      resetCreateForm();
      onNotice("API key created — copy the secret now");
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to create API key");
    } finally {
      setSaving(false);
    }
  };

  const handleEdit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!editingKey || !formData.name.trim()) return;
    setSaving(true);
    try {
      await updateApiKey(secret, editingKey.id, formData);
      if (editSecret.trim()) {
        storeApiKeySecret(editingKey.id, editSecret.trim());
      }
      onNotice(`API key "${formData.name}" updated`);
      setShowEditModal(false);
      setEditingKey(null);
      setEditSecret("");
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to update API key");
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (key: ApiKeyRecord, enabled: boolean) => {
    setTogglingId(key.id);
    try {
      await toggleApiKey(secret, key, enabled);
      onNotice(`API key "${key.name || key.id}" ${enabled ? "enabled" : "disabled"}`);
      onMutated?.();
      await loadUsage();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to update API key");
    } finally {
      setTogglingId(null);
    }
  };

  const handleDelete = (key: ApiKeyRecord) => {
    setConfirmState({
      title: "Delete API key",
      message: `Delete "${key.name || key.id}"? Clients using this key will lose access immediately.`,
      confirmText: "Delete",
      onConfirm: () => {
        void (async () => {
          try {
            await deleteApiKey(secret, key.id);
            onNotice(`API key "${key.name || key.id}" deleted`);
            onMutated?.();
            await loadUsage();
          } catch (error) {
            onError(error instanceof Error ? error.message : "Failed to delete API key");
          }
        })();
      },
    });
  };

  const renderKeyLimitsForm = () => (
    <div className="apis-form-limits">
      <p className="apis-form-section-title">Rate limits</p>
      <div className="inline-fields">
        <Field label="Requests / min">
          <Input
            type="number"
            min={0}
            value={formData.rpm}
            onChange={(event) => setFormData({ ...formData, rpm: Number(event.target.value) })}
          />
        </Field>
        <Field label="Concurrent streams">
          <Input
            type="number"
            min={0}
            value={formData.streams}
            onChange={(event) => setFormData({ ...formData, streams: Number(event.target.value) })}
          />
        </Field>
      </div>
      <div className="inline-fields">
        <Field label="Max input bytes">
          <Input
            type="number"
            min={0}
            value={formData.max_input_bytes}
            onChange={(event) => setFormData({ ...formData, max_input_bytes: Number(event.target.value) })}
          />
        </Field>
        <Field label="Max output tokens">
          <Input
            type="number"
            min={0}
            value={formData.max_output_tokens}
            onChange={(event) => setFormData({ ...formData, max_output_tokens: Number(event.target.value) })}
          />
        </Field>
      </div>
      <div className="inline-fields">
        <Field label="Media jobs">
          <Input
            type="number"
            min={0}
            value={formData.media_jobs}
            onChange={(event) => setFormData({ ...formData, media_jobs: Number(event.target.value) })}
          />
        </Field>
        <Field label="Daily budget (USD)">
          <Input
            type="number"
            min={0}
            step="0.01"
            value={formData.budget_usd_per_day}
            onChange={(event) => setFormData({ ...formData, budget_usd_per_day: Number(event.target.value) })}
          />
        </Field>
      </div>
    </div>
  );

  return (
    <section className="section apis-page">
      <Card className="apis-endpoint-card" pad="md">
        <h2 className="apis-card-title">
          <span className="material-symbols-outlined">api</span>
          API Endpoint
        </h2>

        <div className="endpoint-row-list">
          <EndpointRow label="Local" url={baseUrl} copyId="local_url" copied={copied} onCopy={copy} highlight />
          <TunnelSection secret={secret} apiKeyCount={apiKeys.length} onError={onError} onNotice={onNotice} />
          <EndpointRow
            label="Auth"
            url="Authorization: Bearer <api-key>"
            copyId="auth_header"
            copied={copied}
            onCopy={copy}
          />
          <EndpointRow label="Query" url="?api_key=<api-key>" copyId="auth_query" copied={copied} onCopy={copy} />
        </div>

        {isRemoteHost && apiKeys.length === 0 ? (
          <div className="apis-card-warning">
            <SecurityWarning message="No client API keys configured. Remote clients must authenticate with a valid key." />
          </div>
        ) : null}

        <div className="apis-example-form">
          <Field label="Example API key">
            <ApiKeySelect
              embedded
              apiKeys={apiKeys}
              value={exampleApiKey}
              onChange={setExampleApiKey}
              emptyMessage="No API keys yet. Create one below to generate curl examples."
              missingSecretMessage="Secret for this key is not saved in this browser. Create a new key below and save the secret when it is shown."
            />
          </Field>
          <Field label="Example model" hint="Display names from PPM — curl uses the public model ID">
            <Select value={exampleModel} onChange={(event) => setExampleModel(event.target.value)}>
              {modelOptions.length === 0 ? (
                <option value="">No models yet</option>
              ) : (
                modelOptions.map((model) => (
                  <option key={model.value} value={model.value}>
                    {model.label}
                  </option>
                ))
              )}
            </Select>
          </Field>
        </div>

        <div className="apis-curl-block">
          <div className="apis-curl-head">
            <span>Example request</span>
            <Button
              variant="outline"
              size="sm"
              icon={copied === "curl" ? "check" : "content_copy"}
              onClick={() => copy(curlExample, "curl")}
            >
              Copy curl
            </Button>
          </div>
          <pre>{curlExample}</pre>
        </div>

        <div className="apis-routes-toggle">
          <button type="button" className="apis-routes-toggle-btn" onClick={() => setShowRoutes((current) => !current)}>
            <span className="material-symbols-outlined">{showRoutes ? "expand_less" : "expand_more"}</span>
            Supported routes ({PROXY_ENDPOINTS.length})
          </button>
        </div>

        {showRoutes ? (
          <div className="apis-endpoint-table">
            {PROXY_ENDPOINTS.map((endpoint) => (
              <div className="apis-endpoint-row" key={endpoint.path}>
                <div className="apis-endpoint-path">
                  <code>{endpoint.path}</code>
                  {endpoint.capability ? (
                    <Badge size="sm" variant="default">
                      {endpoint.capability}
                    </Badge>
                  ) : null}
                </div>
                <div className="apis-endpoint-methods">
                  {endpoint.methods.map((method) => (
                    <Badge key={method} size="sm" variant="info">
                      {method}
                    </Badge>
                  ))}
                </div>
                <p>{endpoint.description}</p>
              </div>
            ))}
          </div>
        ) : null}
      </Card>

      <Card className="apis-keys-card" pad="md" id="api-keys">
        <div className="apis-keys-head">
          <h2 className="apis-card-title">
            <span className="material-symbols-outlined">vpn_key</span>
            API Keys
          </h2>
          <Button variant="primary" size="sm" icon="add" onClick={openCreateModal}>
            Create Key
          </Button>
        </div>

        <div className="apis-keys-policy">
          <div>
            <p className="apis-keys-policy-title">Client authentication</p>
            <p className="apis-keys-policy-desc">
              Requests from non-local hosts require a valid API key. Keys are shown only once at creation.
            </p>
          </div>
        </div>

        {apiKeys.length === 0 ? (
          <div className="apis-keys-empty">
            <EmptyState icon="vpn_key" text="No API keys yet" hint="Create your first API key to authenticate clients." />
            <Button variant="primary" icon="add" onClick={openCreateModal}>
              Create Key
            </Button>
          </div>
        ) : (
          <div className="apis-key-list">
            {apiKeys.map((key) => {
              const usage = usageById[key.id];
              const budget = usage?.budget_usd_per_day || key.policy?.limits?.budget_usd_per_day;
              const spent = usage?.cost_usd_today || 0;
              const requests = usage?.requests_today || 0;
              const storedSecret = getStoredApiKeySecret(key.id);

              return (
                <div key={key.id} className={key.enabled ? "apis-key-row" : "apis-key-row is-paused"}>
                  <div className="apis-key-row-main">
                    <p className="apis-key-row-name">{key.name || key.id}</p>
                    <div className="apis-key-row-id">
                      {storedSecret ? (
                        <>
                          <code>{maskApiKeySecret(storedSecret)}</code>
                          <button
                            type="button"
                            className="endpoint-row-copy small"
                            onClick={() => copy(storedSecret, `secret_${key.id}`)}
                            aria-label="Copy API key"
                          >
                            <span className="material-symbols-outlined">
                              {copied === `secret_${key.id}` ? "check" : "content_copy"}
                            </span>
                          </button>
                        </>
                      ) : (
                        <span className="apis-key-secret-missing">Secret not saved in this browser</span>
                      )}
                    </div>
                    <p className="apis-key-row-meta">
                      {key.models?.length ? key.models.join(", ") : "*"} · {formatLimitSummary(key)}
                    </p>
                    <p className="apis-key-row-meta">
                      {loadingUsage ? "Loading usage…" : `${compact(requests)} requests today · ${usd(spent)} spent`}
                      {budget ? ` · ${usd(budget)} budget` : ""}
                    </p>
                    {!key.enabled ? <p className="apis-key-row-paused">Paused</p> : null}
                  </div>
                  <div className="apis-key-row-actions">
                    <Button variant="outline" size="sm" icon="edit" onClick={() => openEditModal(key)}>
                      Edit
                    </Button>
                    <Toggle
                      label=""
                      checked={key.enabled}
                      disabled={togglingId === key.id}
                      onChange={(event) => {
                        const next = event.target.checked;
                        if (key.enabled && !next) {
                          setConfirmState({
                            title: "Pause API key",
                            message: `Pause "${key.name || key.id}"? Clients using this key will stop working immediately.`,
                            confirmText: "Pause",
                            onConfirm: () => void handleToggle(key, false),
                          });
                          return;
                        }
                        void handleToggle(key, next);
                      }}
                    />
                    <button
                      type="button"
                      className="apis-key-delete"
                      onClick={() => handleDelete(key)}
                      aria-label={`Delete ${key.name || key.id}`}
                    >
                      <span className="material-symbols-outlined">delete</span>
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>

      <Modal
        open={showCreateModal}
        onClose={() => {
          setShowCreateModal(false);
          resetCreateForm();
        }}
        title="Create API Key"
        size="md"
      >
        <div className="apis-form">
          <Field label="Key name" required>
            <Input
              placeholder="Production app"
              value={formData.name}
              onChange={(event) => setFormData({ ...formData, name: event.target.value })}
              required
            />
          </Field>

          <button
            type="button"
            className="apis-advanced-toggle"
            onClick={() => setShowAdvancedCreate((current) => !current)}
          >
            <span className="material-symbols-outlined">{showAdvancedCreate ? "expand_less" : "expand_more"}</span>
            Advanced options
          </button>

          {showAdvancedCreate ? (
            <>
              <Field label="Key ID" hint="optional">
                <Input
                  placeholder="my-app-key"
                  value={formData.id}
                  onChange={(event) => setFormData({ ...formData, id: event.target.value })}
                />
              </Field>
              <Field label="Allowed models" hint="* or comma-separated">
                <Input
                  placeholder="*"
                  value={formData.models}
                  onChange={(event) => setFormData({ ...formData, models: event.target.value })}
                />
              </Field>
              <Field label="Team ID" hint="optional">
                <Input
                  placeholder="team-id"
                  value={formData.team}
                  onChange={(event) => setFormData({ ...formData, team: event.target.value })}
                />
              </Field>
              <Field label="Allowed endpoints" hint="optional">
                <Input
                  placeholder="/v1/chat/completions, /v1/models"
                  value={formData.endpoints}
                  onChange={(event) => setFormData({ ...formData, endpoints: event.target.value })}
                />
              </Field>
              {renderKeyLimitsForm()}
            </>
          ) : null}

          <div className="apis-form-actions">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setShowCreateModal(false);
                resetCreateForm();
              }}
            >
              Cancel
            </Button>
            <Button variant="primary" icon="add" disabled={saving || !formData.name.trim()} onClick={() => void handleCreate()}>
              Create
            </Button>
          </div>
        </div>
      </Modal>

      <Modal open={!!createdSecret} onClose={() => setCreatedSecret(null)} title="API Key Created" size="md">
        <div className="apis-created-modal">
          <div className="apis-created-warning">
            <p className="title">Save this key now!</p>
            <p>This is the only time you will see the full secret. Store it securely.</p>
          </div>
          <div className="apis-created-key-row">
            <Input value={createdSecret || ""} readOnly className="endpoint-row-input" />
            <Button
              variant="outline"
              icon={copied === "created_key" ? "check" : "content_copy"}
              onClick={() => createdSecret && copy(createdSecret, "created_key")}
            >
              {copied === "created_key" ? "Copied" : "Copy"}
            </Button>
          </div>
          <Button variant="primary" onClick={() => setCreatedSecret(null)}>
            Done
          </Button>
        </div>
      </Modal>

      <Modal
        open={showEditModal}
        onClose={() => {
          setShowEditModal(false);
          setEditingKey(null);
          setEditSecret("");
        }}
        title="Edit API key"
        size="lg"
      >
        <form className="apis-form" onSubmit={handleEdit}>
          <Field label="Name" required>
            <Input
              value={formData.name}
              onChange={(event) => setFormData({ ...formData, name: event.target.value })}
              required
            />
          </Field>
          <Field label="Allowed models" hint="* or comma-separated">
            <Input
              value={formData.models}
              onChange={(event) => setFormData({ ...formData, models: event.target.value })}
            />
          </Field>
          <Field
            label="API key secret"
            hint="Stored only in this browser for copy and examples. Paste the client secret if you imported or created this key elsewhere."
          >
            <Input
              type="password"
              autoComplete="off"
              placeholder="tp_… or paste secret from backup"
              value={editSecret}
              onChange={(event) => setEditSecret(event.target.value)}
            />
          </Field>
          <Field label="Team ID" hint="optional">
            <Input value={formData.team} onChange={(event) => setFormData({ ...formData, team: event.target.value })} />
          </Field>
          <Field label="Allowed endpoints" hint="optional">
            <Input
              value={formData.endpoints}
              onChange={(event) => setFormData({ ...formData, endpoints: event.target.value })}
            />
          </Field>
          {renderKeyLimitsForm()}
          <Toggle
            label="Key enabled"
            checked={formData.enabled}
            onChange={(event) => setFormData({ ...formData, enabled: event.target.checked })}
          />
          <div className="apis-form-actions">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setShowEditModal(false);
                setEditingKey(null);
                setEditSecret("");
              }}
            >
              Cancel
            </Button>
            <Button type="submit" variant="primary" icon="save" disabled={saving || !formData.name.trim()}>
              Save changes
            </Button>
          </div>
        </form>
      </Modal>

      <ConfirmDialog
        open={confirmState !== null}
        title={confirmState?.title || ""}
        message={confirmState?.message || ""}
        variant="danger"
        confirmText={confirmState?.confirmText || "Confirm"}
        onClose={() => setConfirmState(null)}
        onConfirm={() => {
          confirmState?.onConfirm();
          setConfirmState(null);
        }}
      />
    </section>
  );
}
