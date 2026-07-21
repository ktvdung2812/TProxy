import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { getStoredApiKeySecret, storeApiKeySecret } from "../../lib/apiKeySecrets";
import { Field, Input, Select } from "../ui";

export type ApiKeyOption = {
  id: string;
  name: string;
  enabled?: boolean;
};

type Props = {
  apiKeys: ApiKeyOption[];
  value: string;
  onChange: (secret: string) => void;
  onSelectedIdChange?: (id: string) => void;
  /** Render without outer Field wrapper (parent supplies label). */
  embedded?: boolean;
  /** Show a text field for the API key secret (CLI Tools guide). */
  showSecretField?: boolean;
  emptyMessage?: string;
  missingSecretMessage?: string;
};

function resolveSelectedId(secret: string, enabledKeys: ApiKeyOption[]): string {
  if (enabledKeys.length === 0) return "";
  if (secret) {
    const matched = enabledKeys.find((key) => getStoredApiKeySecret(key.id) === secret);
    if (matched) return matched.id;
  }
  const firstWithSecret = enabledKeys.find((key) => getStoredApiKeySecret(key.id));
  return firstWithSecret?.id ?? enabledKeys[0].id;
}

export function ApiKeySelect({
  apiKeys,
  value,
  onChange,
  onSelectedIdChange,
  embedded = false,
  showSecretField = false,
  emptyMessage,
  missingSecretMessage,
}: Props) {
  const enabledKeys = useMemo(() => apiKeys.filter((key) => key.enabled !== false), [apiKeys]);
  const [selectedId, setSelectedId] = useState(() => resolveSelectedId(value, enabledKeys));

  useEffect(() => {
    if (enabledKeys.length === 0) return;
    const nextId = resolveSelectedId(value, enabledKeys);
    setSelectedId(nextId);
    onSelectedIdChange?.(nextId);
    const stored = getStoredApiKeySecret(nextId);
    if (stored && stored !== value) onChange(stored);
  }, [enabledKeys, onChange, onSelectedIdChange, value]);

  useEffect(() => {
    if (value || enabledKeys.length === 0) return;
    const nextId = resolveSelectedId("", enabledKeys);
    setSelectedId(nextId);
    onSelectedIdChange?.(nextId);
    const stored = getStoredApiKeySecret(nextId);
    if (stored) onChange(stored);
  }, [enabledKeys, onChange, onSelectedIdChange, value]);

  const handleSelect = (id: string) => {
    setSelectedId(id);
    onSelectedIdChange?.(id);
    const stored = getStoredApiKeySecret(id) ?? "";
    onChange(stored);
  };

  const handleSecretChange = (next: string) => {
    onChange(next);
    if (selectedId && next.trim()) {
      storeApiKeySecret(selectedId, next.trim());
    }
  };

  const selectedKey = enabledKeys.find((key) => key.id === selectedId);
  const hasSecret = selectedId ? !!getStoredApiKeySecret(selectedId) : false;

  const stackClass = embedded ? "api-key-select-embedded" : "cli-tool-field-stack";

  if (enabledKeys.length === 0) {
    return (
      <div className={stackClass}>
        <p className="cli-tool-hint">
          {emptyMessage ?? (
            <>
              No API keys yet. Create one in <Link to="/apis">APIs</Link> before configuring this CLI tool.
            </>
          )}
        </p>
      </div>
    );
  }

  const select = (
    <Select value={selectedId} onChange={(event) => handleSelect(event.target.value)}>
      {enabledKeys.map((key) => (
        <option key={key.id} value={key.id}>
          {key.name && key.name !== key.id ? `${key.name} (${key.id})` : key.id}
        </option>
      ))}
    </Select>
  );

  return (
    <div className={stackClass}>
      {embedded ? select : <Field label="Select key">{select}</Field>}
      {showSecretField ? (
        <Field
          label="API key value"
          hint={
            hasSecret
              ? "Used in command previews below. Saved in this browser."
              : "Paste the secret shown when the key was created, or it will be resolved from TPROXY_API_KEY when available."
          }
        >
          <Input
            value={value}
            onChange={(event) => handleSecretChange(event.target.value)}
            placeholder="tp_..."
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ) : hasSecret ? (
        embedded ? null : (
          <p className="cli-tool-hint">
            Using key <strong>{selectedKey?.name || selectedKey?.id}</strong>. Manage keys in{" "}
            <Link to="/apis">APIs</Link>.
          </p>
        )
      ) : (
        <p className="cli-tool-hint">
          {missingSecretMessage ?? (
            <>
              Secret for this key is not saved in this browser. Create a new key in{" "}
              <Link to="/apis">APIs</Link> and save the secret when it is shown.
            </>
          )}
        </p>
      )}
    </div>
  );
}
