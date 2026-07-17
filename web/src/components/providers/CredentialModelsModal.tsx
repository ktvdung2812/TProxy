import { useEffect, useMemo, useState } from "react";
import { Badge, Button, EmptyState, Input, Modal } from "../ui";
import { discoverCredentialModels, type DiscoveredModel } from "./api";
import type { Credential } from "./types";

type Props = {
  open: boolean;
  credential: Credential | null;
  secret: string;
  onClose: () => void;
};

export function CredentialModelsModal({ open, credential, secret, onClose }: Props) {
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<DiscoveredModel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");

  useEffect(() => {
    if (!open || !credential) {
      setModels([]);
      setError(null);
      setCopied(null);
      setSearchQuery("");
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
        setError(cause instanceof Error ? cause.message : "Model discovery failed");
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
          <Input
            icon="search"
            placeholder="Search models..."
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            style={{ width: "100%", maxWidth: 240 }}
            aria-label="Search models"
          />
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
          <p style={{ margin: "0 0 12px", fontSize: 12, color: "var(--color-text-muted)" }}>
            {summaryText}
            {error ? ` · ${error}` : ""}
          </p>
          <div className="model-table-wrap">
            <table className="model-table">
              <thead>
                <tr>
                  <th>Model</th>
                  <th>Provider</th>
                  <th>Capabilities</th>
                  <th style={{ textAlign: "right" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredModels.map((model) => (
                  <tr key={model.id}>
                    <td>
                      <div className="model-table-identity">
                        <div className="model-table-name">{model.name?.trim() || model.id}</div>
                        {model.name?.trim() && model.name.trim() !== model.id ? (
                          <code>{model.id}</code>
                        ) : null}
                      </div>
                    </td>
                    <td>{model.owned_by || "—"}</td>
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
                          icon={copied === model.id ? "check" : "content_copy"}
                          onClick={() => handleCopy(model.id)}
                          aria-label="Copy model id"
                        />
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Modal>
  );
}
