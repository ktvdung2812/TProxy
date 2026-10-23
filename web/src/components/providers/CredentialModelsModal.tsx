import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, EmptyState, Input, Modal } from "../ui";
import { discoverCredentialModels, testModel, type DiscoveredModel } from "./api";
import type { Credential } from "./types";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";

type Props = {
  open: boolean;
  credential: Credential | null;
  providerId: string;
  secret: string;
  onClose: () => void;
};

export function CredentialModelsModal({ open, credential, providerId, secret, onClose }: Props) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
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

  const title = maskAccountText(credential.email || credential.label || credential.id, masked);
  const subtitle = credential.email || credential.label ? maskAccountText(credential.id, masked) : undefined;

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
          setTestError(cause instanceof Error ? cause.message : t("providers.models.testFailedFor", { id: model.id }));
        }
      }
    } finally {
      setTestingModelId(null);
      setTestingAll(false);
    }
  };

  const summaryText = normalizedQuery
    ? t("providers.modelsSectionCardSubtitleFiltered", { filtered: filteredModels.length, total: models.length })
    : t("providers.models.onAccount", { count: models.length });

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
          <div className="pv-modal-tools">
            <Button
              variant="outline"
              size="sm"
              icon={testingAll ? "progress_activity" : "science"}
              onClick={() => void handleTestAllModels()}
              disabled={testingAll || !!testingModelId}
              aria-label={t("providers.testAllModels")}
              title={t("providers.testAllModels")}
            >
              {testingAll ? t("providers.testing") : t("providers.testAll")}
            </Button>
            <Input
              icon="search"
              placeholder={t("providers.models.searchPlaceholder")}
              value={searchQuery}
              onChange={(event) => setSearchQuery(event.target.value)}
              aria-label={t("providers.models.searchPlaceholder")}
            />
          </div>
        ) : null
      }
      footer={
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
      }
    >
      {loading ? (
        <EmptyState icon="progress_activity" text={t("providers.models.loadingAccount")} />
      ) : error && models.length === 0 ? (
        <EmptyState icon="error" text={t("providers.credentialModelsLoadFailed")} hint={error} />
      ) : models.length === 0 ? (
        <EmptyState
          icon="apps"
          text={t("providers.models.noneOnAccount")}
          hint={t("providers.credentialModelsNoDiscovery")}
        />
      ) : normalizedQuery && filteredModels.length === 0 ? (
        <EmptyState
          icon="search"
          text={t("providers.modelsSectionNoMatch", { query: searchQuery.trim() })}
          hint={t("providers.modelsSectionNoMatchHint")}
        />
      ) : (
        <div>
          {testError ? (
            <p className="pv-form-error" role="alert">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              {testError}
            </p>
          ) : null}
          <p className="pv-modal-summary">
            {summaryText}
            {error ? ` · ${error}` : ""}
          </p>
          <div className="model-table-wrap">
            <table className="model-table">
              <thead>
                <tr>
                  <th>{t("providers.colModel")}</th>
                  <th>{t("providers.colCapabilities")}</th>
                  <th className="pv-th-end">{t("providers.colActions")}</th>
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
                          aria-label={t("providers.testModel")}
                          title={isTesting ? t("providers.testing") : t("providers.testModel")}
                        />
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={copied === model.id ? "check" : "content_copy"}
                          onClick={() => void handleCopy(model.id)}
                          aria-label={t("providers.copyModelId")}
                          title={t("providers.copyModelId")}
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
