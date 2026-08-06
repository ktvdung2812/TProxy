import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Card, EmptyState, Input } from "../ui";
import { discoverProviderModels, testModel } from "./api";
import type { DiscoveredModel } from "./api";
import type { Credential } from "./types";

type Props = {
  providerId: string;
  credentials: Credential[];
  secret: string;
  discoverNonce?: number;
};

/**
 * Models card for a provider detail page — upstream models discovered from accounts.
 */
export function ModelsSection({
  providerId,
  credentials,
  secret,
  discoverNonce = 0,
}: Props) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState<string | null>(null);
  const [discovering, setDiscovering] = useState(false);
  const [availableModels, setAvailableModels] = useState<DiscoveredModel[]>([]);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [modelTestResults, setModelTestResults] = useState<Record<string, "ok" | "error">>({});
  const [testingModelId, setTestingModelId] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);
  const [testError, setTestError] = useState("");

  const enabledCredentials = useMemo(
    () => credentials.filter((credential) => credential.enabled),
    [credentials],
  );

  const normalizedQuery = searchQuery.trim().toLowerCase();

  const filteredAvailableModels = useMemo(() => {
    if (!normalizedQuery) return availableModels;
    return availableModels.filter((model) => {
      const haystack = [model.id, model.name, model.owned_by, ...(model.capabilities || [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(normalizedQuery);
    });
  }, [availableModels, normalizedQuery]);

  useEffect(() => {
    if (enabledCredentials.length === 0) {
      setAvailableModels([]);
      setDiscoveryError(null);
      return;
    }

    let cancelled = false;
    setDiscovering(true);
    setDiscoveryError(null);

    discoverProviderModels(secret, providerId, discoverNonce > 0)
      .then((result) => {
        if (cancelled) return;
        setAvailableModels(result.data || []);
        if (result.error?.message) {
          setDiscoveryError(result.error.message);
        }
      })
      .catch((cause) => {
        if (cancelled) return;
        setAvailableModels([]);
        setDiscoveryError(cause instanceof Error ? cause.message : t("providers.modelDiscoveryFailed"));
      })
      .finally(() => {
        if (!cancelled) setDiscovering(false);
      });

    return () => {
      cancelled = true;
    };
  }, [providerId, secret, enabledCredentials.length, discoverNonce]);

  const handleCopy = async (text: string, key: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      window.setTimeout(() => setCopied(null), 1500);
    } catch {
      /* clipboard blocked */
    }
  };

  const runModelTest = async (model: DiscoveredModel) => {
    const result = await testModel(secret, {
      provider_id: providerId,
      model_id: model.id,
      kind: "llm",
      credential_ids: model.credential_ids,
    });
    setModelTestResults((prev) => ({ ...prev, [model.id]: result.ok ? "ok" : "error" }));
    if (!result.ok) {
      const latency = result.latency_ms ? ` (${result.latency_ms} ms)` : "";
      setTestError(`${model.id}: ${result.error || t("providers.modelNotReachable")}${latency}`);
    }
    return result.ok;
  };

  const handleTestUpstreamModel = async (model: DiscoveredModel) => {
    if (testingModelId || testingAll) return;
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
    if (testingModelId || testingAll || availableModels.length === 0) return;
    setTestingAll(true);
    setTestError("");
    try {
      for (const model of availableModels) {
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

  const accountCount = enabledCredentials.length;
  const availableSubtitle = discovering
    ? t("providers.discoveringFromAccounts")
    : `${availableModels.length} model${availableModels.length === 1 ? "" : "s"} across ${accountCount} account${accountCount === 1 ? "" : "s"}`;

  const cardSubtitle = discovering
    ? t("providers.loadingFromAccounts")
    : enabledCredentials.length === 0
      ? "Connect an account to discover upstream models"
      : normalizedQuery
        ? `${filteredAvailableModels.length} of ${availableModels.length} matching`
        : `${availableModels.length} upstream model${availableModels.length === 1 ? "" : "s"}`;

  return (
    <Card
      pad="md"
      className="section"
      title="Models"
      subtitle={cardSubtitle}
      icon="apps"
      action={
        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", justifyContent: "flex-end" }}>
          <Button
            variant="outline"
            size="sm"
            icon={testingAll ? "progress_activity" : "science"}
            onClick={() => void handleTestAllModels()}
            disabled={testingAll || !!testingModelId || availableModels.length === 0 || discovering}
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
            style={{ width: 220 }}
            aria-label="Search models"
          />
        </div>
      }
    >
      {enabledCredentials.length === 0 ? (
        <EmptyState
          icon="key_off"
          text="No accounts connected yet."
          hint="Add a credential to discover upstream models available from this provider."
        />
      ) : discovering && availableModels.length === 0 ? (
        <EmptyState icon="progress_activity" text="Discovering models from all accounts..." />
      ) : availableModels.length === 0 && !normalizedQuery ? (
        <EmptyState
          icon="apps"
          text="No models discovered from connected accounts."
          hint={discoveryError || "Check account health or try Discover models again."}
        />
      ) : normalizedQuery && filteredAvailableModels.length === 0 ? (
        <EmptyState
          icon="search"
          text={`No models match "${searchQuery.trim()}".`}
          hint="Try a different model id, name, or capability."
        />
      ) : filteredAvailableModels.length > 0 ? (
        <div>
          {testError ? (
            <p style={{ margin: "0 0 8px", fontSize: 12, color: "var(--color-danger)", wordBreak: "break-word" }}>
              {testError}
            </p>
          ) : null}
          <div className="model-table-wrap">
            <table className="model-table">
              <thead>
                <tr>
                  <th>Model</th>
                  <th>Capabilities</th>
                  <th>Accounts</th>
                  <th style={{ textAlign: "right" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredAvailableModels.map((model) => {
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
                        {(model.credential_ids?.length ?? 0) > 0 ? (
                          <Badge variant="info" size="sm">
                            {model.credential_ids!.length}
                          </Badge>
                        ) : (
                          "—"
                        )}
                      </td>
                      <td>
                        <div className="model-table-actions">
                          <Button
                            variant="ghost"
                            size="sm"
                            icon={isTesting ? "progress_activity" : testStatus === "ok" ? "check_circle" : testStatus === "error" ? "cancel" : "science"}
                            onClick={() => void handleTestUpstreamModel(model)}
                            disabled={isTesting || testingAll || enabledCredentials.length === 0}
                            aria-label="Test model"
                            title={isTesting ? "Testing..." : "Test model"}
                          />
                          <Button
                            variant="ghost"
                            size="sm"
                            icon={copied === model.id ? "check" : "content_copy"}
                            onClick={() => handleCopy(model.id, model.id)}
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
      ) : normalizedQuery ? (
        <p style={{ margin: 0, fontSize: 13, color: "var(--color-text-muted)" }}>
          No upstream models match your search.
        </p>
      ) : null}

      {enabledCredentials.length > 0 && (
        <p style={{ margin: "12px 0 0", fontSize: 12, color: "var(--color-text-muted)" }}>
          {availableSubtitle}
        </p>
      )}
    </Card>
  );
}
