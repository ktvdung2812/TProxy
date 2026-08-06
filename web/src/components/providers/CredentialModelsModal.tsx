import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, EmptyState, Input, Modal } from "../ui";
import { discoverCredentialModels, testModel, type DiscoveredModel } from "./api";
import type { Credential } from "./types";

type Props = {
  open: boolean;
  credential: Credential | null;
  providerId: string;
  secret: string;
  onClose: () => void;
};

export function CredentialModelsModal({ open, credential, providerId, secret, onClose }: Props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<DiscoveredModel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [modelTestResults, setModelTestResults] = useState<Record<string, "ok" | "error">>({});
  const [testingModelId, setTestingModelId] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);
  const [testError, setTestError] = useState("");

  useEffect(() => {
    if (!open || !credential) {
      setModels([]);
      setError(null);
      setCopied(null);
      setSearchQuery("");
      setModelTestResults({});
      setTestingModelId(null);
      setTestingAll(false);
      setTestError("");
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError(null);
    setSearchQuery("");

    discoverCredentialModels(secret, credential.id)
      .then((result) => {
        if (cancelled) return;
        setModels(result.data || []);
        if (result.error?.message) {
          setError(result.error.message);
        }
      })
      .catch((cause) => {
        if (cancelled) return;
        setModels([]);
        setError(cause instanceof Error ? cause.message : t("providers.modelDiscoveryFailed"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [open, credential, secret]);

  const normalizedQuery = searchQuery.trim().toLowerCase();

  const filteredModels = useMemo(() => {
    if (!normalizedQuery) return models;
    return models.filter((model) => {
      const haystack = [model.id, model.name, model.owned_by, ...(model.capabilities || [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(normalizedQuery);
    });
  }, [models, normalizedQuery]);

  if (!credential) return null;

  const title = credential.email || credential.label || credential.id;
  const subtitle = credential.email || credential.label ? credential.id : undefined;

  const handleCopy = async (modelId: string) => {
    try {
      await navigator.clipboard.writeText(modelId);
      setCopied(modelId);
      window.setTimeout(() => setCopied(null), 1500);
    } catch {
      /* clipboard blocked */
    }
  };

  const runModelTest = async (model: DiscoveredModel) => {
    if (!credential) return false;
    const result = await testModel(secret, {
      provider_id: providerId,
      model_id: model.id,
      kind: "llm",
      credential_id: credential.id,
    });
    setModelTestResults((prev) => ({ ...prev, [model.id]: result.ok ? "ok" : "error" }));
    if (!result.ok) {
      const latency = result.latency_ms ? ` (${result.latency_ms} ms)` : "";
      setTestError(`${model.id}: ${result.error || t("providers.modelNotReachable")}${latency}`);
    }
    return result.ok;
  };

  const handleTestModel = async (model: DiscoveredModel) => {
    if (!credential || testingModelId || testingAll) return;
    setTestingModelId(model.id);
    setTestError("");
    try {
      await runModelTest(model);
    } catch (cause) {
      setModelTestResults((prev) => ({ ...prev, [model.id]: "error" }));
      setTestError(cause instanceof Error ? cause.message : t("providers.testFailed"));
    } finally {
      setTestingModelId(null);
    }
  };

  const handleTestAllModels = async () => {
    if (!credential || testingModelId || testingAll || models.length === 0) return;
    setTestingAll(true);
    setTestError("");
    try {
      for (const model of models) {
        setTestingModelId(model.id);
        try {
          await runModelTest(model);
        } catch (cause) {
          setModelTestResults((prev) => ({ ...prev, [model.id]: "error" }));
          setTestError(cause instanceof Error ? cause.message : `Test failed for ${model.id}`);
        }
      }
    } finally {
      setTestingModelId(null);
      setTestingAll(false);
    }
  };

  const summaryText = normalizedQuery
    ? `${filteredModels.length} of ${models.length} model${models.length === 1 ? "" : "s"}`
    : `${models.length} model${models.length === 1 ? "" : "s"} available on this account`;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      subtitle={subtitle}
      icon="apps"
      size="lg"
      headerAction={
        !loading && models.length > 0 ? (
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", justifyContent: "flex-end" }}>
            <Button
              variant="outline"
              size="sm"
              icon={testingAll ? "progress_activity" : "science"}
              onClick={() => void handleTestAllModels()}
              disabled={testingAll || !!testingModelId}
              aria-label={t("providers.testAllModels")}
              title={t("providers.testAllModels")}
            >
              {testingAll ? "Testing..." : "Test all"}
            </Button>
            <Input
              icon="search"
              placeholder="Search models..."
              value={searchQuery}
              onChange={(event) => setSearchQuery(event.target.value)}
              style={{ width: "100%", maxWidth: 240 }}
              aria-label="Search models"
            />
          </div>
        ) : null
      }
      footer={
        <Button variant="secondary" onClick={onClose}>
          Close
        </Button>
      }
    >
      {loading ? (
        <EmptyState icon="progress_activity" text="Loading supported models..." />
      ) : error && models.length === 0 ? (
        <EmptyState icon="error" text="Could not load models for this account." hint={error} />
      ) : models.length === 0 ? (
        <EmptyState
          icon="apps"
          text="No models discovered for this account."
          hint="The upstream did not return any models, or this provider type does not support discovery."
        />
      ) : normalizedQuery && filteredModels.length === 0 ? (
        <EmptyState
          icon="search"
          text={`No models match "${searchQuery.trim()}".`}
          hint="Try a different model id, name, provider, or capability."
        />
      ) : (
        <div>
          {testError ? (
            <p style={{ margin: "0 0 8px", fontSize: 12, color: "var(--color-danger)", wordBreak: "break-word" }}>
              {testError}
            </p>
          ) : null}
          <p style={{ margin: "0 0 12px", fontSize: 12, color: "var(--color-text-muted)" }}>
            {summaryText}
            {error ? ` · ${error}` : ""}
          </p>
          <div className="model-table-wrap">
            <table className="model-table">
              <thead>
                <tr>
                  <th>Model</th>
                  <th>Capabilities</th>
                  <th style={{ textAlign: "right" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredModels.map((model) => {
                  const testStatus = modelTestResults[model.id];
                  const isTesting = testingModelId === model.id;
                  return (
                  <tr
                    key={model.id}
                    className={
                      testStatus === "ok"
                        ? "model-table-test-ok"
                        : testStatus === "error"
                          ? "model-table-test-error"
                          : undefined
                    }
                  >
                    <td>
                      <div className="model-table-identity">
                        <div className="model-table-name">{model.name?.trim() || model.id}</div>
                        {model.name?.trim() && model.name.trim() !== model.id ? (
                          <code>{model.id}</code>
                        ) : null}
                      </div>
                    </td>
                    <td>
                      <div className="model-table-capabilities">
                        {model.capabilities?.map((cap) => (
                          <Badge key={cap} variant="default" size="sm">{cap}</Badge>
                        ))}
                      </div>
                    </td>
                    <td>
                      <div className="model-table-actions">
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={isTesting ? "progress_activity" : testStatus === "ok" ? "check_circle" : testStatus === "error" ? "cancel" : "science"}
                          onClick={() => void handleTestModel(model)}
                          disabled={isTesting || testingAll}
                          aria-label="Test model"
                          title={isTesting ? "Testing..." : "Test model"}
                        />
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={copied === model.id ? "check" : "content_copy"}
                          onClick={() => void handleCopy(model.id)}
                          aria-label="Copy model id"
                        />
                      </div>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Modal>
  );
}
