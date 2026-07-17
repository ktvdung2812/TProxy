import { useEffect, useMemo, useState } from "react";
import { Badge, Button, Card, EmptyState, Input } from "../ui";
import { discoverProviderModels, testModel } from "./api";
import type { DiscoveredModel } from "./api";
import type { Credential, PublicModel, RouteTarget } from "./types";

type Props = {
  providerId: string;
  credentials: Credential[];
  models: PublicModel[];
  routes: RouteTarget[];
  secret: string;
  discoverNonce?: number;
};

/**
 * Models card for a provider detail page.
 *
 * Surfaces:
 *   - Upstream models available across all credentials (auto-discovered).
 *   - Public virtual models routed to this provider.
 */
export function ModelsSection({
  providerId,
  credentials,
  models,
  routes,
  secret,
  discoverNonce = 0,
}: Props) {
  const [copied, setCopied] = useState<string | null>(null);
  const [discovering, setDiscovering] = useState(false);
  const [availableModels, setAvailableModels] = useState<DiscoveredModel[]>([]);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [modelTestResults, setModelTestResults] = useState<Record<string, "ok" | "error">>({});
  const [testingModelId, setTestingModelId] = useState<string | null>(null);
  const [testError, setTestError] = useState("");

  const enabledCredentials = useMemo(
    () => credentials.filter((credential) => credential.enabled),
    [credentials],
  );

  // Routes that point at this provider, joined with the public model metadata.
  const routedModels = useMemo(() => {
    const modelById = new Map(models.map((m) => [m.ID, m]));
    return routes
      .filter((r) => r.ProviderID === providerId)
      .map((route) => ({ route, model: modelById.get(route.PublicModelID) }))
      .sort((a, b) => (a.route.Priority || 0) - (b.route.Priority || 0));
  }, [routes, models, providerId]);

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

  const filteredRoutedModels = useMemo(() => {
    if (!normalizedQuery) return routedModels;
    return routedModels.filter(({ route, model }) => {
      const haystack = [
        route.PublicModelID,
        route.UpstreamModel,
        model?.DisplayName,
        model?.ID,
        ...(model?.Capabilities || []),
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(normalizedQuery);
    });
  }, [routedModels, normalizedQuery]);

  useEffect(() => {
    if (enabledCredentials.length === 0) {
      setAvailableModels([]);
      setDiscoveryError(null);
      return;
    }

    let cancelled = false;
    setDiscovering(true);
    setDiscoveryError(null);

    discoverProviderModels(secret, providerId)
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
        setDiscoveryError(cause instanceof Error ? cause.message : "Model discovery failed");
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

  const inferModelKind = (capabilities?: string[]) => {
    if (capabilities?.includes("embedding")) return "embedding";
    if (capabilities?.includes("image-output")) return "image";
    if (capabilities?.includes("stt")) return "stt";
    return "llm";
  };

  const handleTestUpstreamModel = async (model: DiscoveredModel) => {
    if (testingModelId) return;
    setTestingModelId(model.id);
    setTestError("");
    try {
      const result = await testModel(secret, {
        provider_id: providerId,
        model_id: model.id,
        kind: inferModelKind(model.capabilities),
        credential_ids: model.credential_ids,
      });
      setModelTestResults((prev) => ({ ...prev, [model.id]: result.ok ? "ok" : "error" }));
      if (!result.ok) {
        const latency = result.latency_ms ? ` (${result.latency_ms} ms)` : "";
        setTestError((result.error || "Model not reachable") + latency);
      }
    } catch (cause) {
      setModelTestResults((prev) => ({ ...prev, [model.id]: "error" }));
      setTestError(cause instanceof Error ? cause.message : "Test failed");
    } finally {
      setTestingModelId(null);
    }
  };

  const handleTestRoutedModel = async (publicModelId: string, key: string) => {
    if (testingModelId) return;
    setTestingModelId(key);
    setTestError("");
    try {
      const result = await testModel(secret, { public_model_id: publicModelId });
      setModelTestResults((prev) => ({ ...prev, [key]: result.ok ? "ok" : "error" }));
      if (!result.ok) {
        const latency = result.latency_ms ? ` (${result.latency_ms} ms)` : "";
        setTestError((result.error || "Model not reachable") + latency);
      }
    } catch (cause) {
      setModelTestResults((prev) => ({ ...prev, [key]: "error" }));
      setTestError(cause instanceof Error ? cause.message : "Test failed");
    } finally {
      setTestingModelId(null);
    }
  };

  const accountCount = enabledCredentials.length;
  const availableSubtitle = discovering
    ? "Discovering models from accounts..."
    : `${availableModels.length} model${availableModels.length === 1 ? "" : "s"} across ${accountCount} account${accountCount === 1 ? "" : "s"}`;

  const cardSubtitle = discovering
    ? "Loading models from all accounts..."
    : enabledCredentials.length === 0
      ? "Connect an account to discover upstream models"
      : normalizedQuery
        ? `${filteredAvailableModels.length} of ${availableModels.length} available · ${filteredRoutedModels.length} of ${routedModels.length} routed`
        : `${availableModels.length} available model${availableModels.length === 1 ? "" : "s"} · ${routedModels.length} routed virtual model${routedModels.length === 1 ? "" : "s"}`;

  return (
    <Card
      pad="md"
      className="section"
      title="Models"
      subtitle={cardSubtitle}
      icon="apps"
      action={
        <Input
          icon="search"
          placeholder="Search models..."
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.target.value)}
          style={{ width: 220 }}
          aria-label="Search models"
        />
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
      ) : normalizedQuery && filteredAvailableModels.length === 0 && filteredRoutedModels.length === 0 ? (
        <EmptyState
          icon="search"
          text={`No models match "${searchQuery.trim()}".`}
          hint="Try a different model id, name, upstream id, or capability."
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
                            onClick={() => handleTestUpstreamModel(model)}
                            disabled={isTesting || enabledCredentials.length === 0}
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

      {filteredRoutedModels.length > 0 && (
        <div style={{ marginTop: 16, paddingTop: 16, borderTop: "1px solid var(--color-border-subtle)" }}>
          <p style={{ margin: "0 0 8px", fontSize: 12, fontWeight: 600, color: "var(--color-text-muted)" }}>
            Routed virtual models
          </p>
          <div className="model-table-wrap">
            <table className="model-table">
              <thead>
                <tr>
                  <th>Virtual model</th>
                  <th>Public ID</th>
                  <th>Upstream</th>
                  <th>Capabilities</th>
                  <th>Priority</th>
                  <th style={{ textAlign: "right" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredRoutedModels.map(({ route, model }) => {
                  const testStatus = modelTestResults[route.ID];
                  const isTesting = testingModelId === route.ID;
                  return (
                    <tr
                      key={route.ID}
                      className={
                        testStatus === "ok"
                          ? "model-table-test-ok"
                          : testStatus === "error"
                            ? "model-table-test-error"
                            : undefined
                      }
                    >
                      <td className="model-table-name">{model?.DisplayName || route.PublicModelID}</td>
                      <td><code>{route.PublicModelID}</code></td>
                      <td><code>{route.UpstreamModel}</code></td>
                      <td>
                        <div className="model-table-capabilities">
                          {model?.Capabilities?.map((cap) => (
                            <Badge key={cap} variant="default" size="sm">{cap}</Badge>
                          ))}
                          {!route.Enabled && <Badge variant="warning" size="sm">disabled</Badge>}
                        </div>
                      </td>
                      <td>
                        <Badge variant="primary" size="sm">P{route.Priority}</Badge>
                      </td>
                      <td>
                        <div className="model-table-actions">
                          <Button
                            variant="ghost"
                            size="sm"
                            icon={isTesting ? "progress_activity" : testStatus === "ok" ? "check_circle" : testStatus === "error" ? "cancel" : "science"}
                            onClick={() => handleTestRoutedModel(route.PublicModelID, route.ID)}
                            disabled={isTesting}
                            aria-label="Test routed model"
                            title={isTesting ? "Testing..." : "Test model"}
                          />
                          <Button
                            variant="ghost"
                            size="sm"
                            icon={copied === route.ID ? "check" : "content_copy"}
                            onClick={() => handleCopy(route.PublicModelID, route.ID)}
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

      {filteredRoutedModels.length === 0 && availableModels.length > 0 && !normalizedQuery && (
        <EmptyState
          icon="route"
          text="No virtual models route to this provider yet."
          hint="Create a model with a route targeting this provider from the Provider Priority Manager page."
        />
      )}
    </Card>
  );
}
