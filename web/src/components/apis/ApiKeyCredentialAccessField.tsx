import { useMemo } from "react";
import { Badge, Button, Toggle } from "../ui";
import type { ApiKeyCombo, ApiKeyCredential, ApiKeyProvider, ApiKeyRoute } from "./types";

type Props = {
  value: string[];
  models: string;
  providers: ApiKeyProvider[];
  credentials: Record<string, ApiKeyCredential[]>;
  routes: Record<string, ApiKeyRoute[]>;
  combos: ApiKeyCombo[];
  onChange: (credentialIDs: string[]) => void;
};

function parseModelSelection(value: string): string[] {
  return value
    .split(",")
    .map((modelID) => modelID.trim())
    .filter(Boolean);
}

function unique(values: Iterable<string>): string[] {
  return [...new Set(values)];
}

export function ApiKeyCredentialAccessField({ value, models, providers, credentials, routes, combos, onChange }: Props) {
  const selectedCredentialIDs = useMemo(() => new Set(value), [value]);
  const selectedModels = useMemo(() => parseModelSelection(models), [models]);

  const compatibleProviderIDs = useMemo(() => {
    const routeTargets: ApiKeyRoute[] = [];
    if (selectedModels.includes("*")) {
      for (const modelRoutes of Object.values(routes)) routeTargets.push(...modelRoutes);
    } else {
      const combosByID = new Map(combos.map((combo) => [combo.id, combo]));
      for (const modelID of selectedModels) {
        const combo = combosByID.get(modelID);
        if (!combo) {
          routeTargets.push(...(routes[modelID] || []));
          continue;
        }
        for (const item of combo.items || []) {
          const modelRoutes = routes[item.public_model_id] || [];
          routeTargets.push(...(item.route_target_id ? modelRoutes.filter((route) => route.ID === item.route_target_id) : modelRoutes));
        }
      }
    }
    return new Set(routeTargets.filter((route) => route.Enabled).map((route) => route.ProviderID));
  }, [combos, routes, selectedModels]);

  const compatibleProviders = useMemo(
    () => providers.filter((provider) => provider.Enabled && compatibleProviderIDs.has(provider.ID)),
    [compatibleProviderIDs, providers],
  );
  const compatibleCredentialIDs = useMemo(
    () => new Set(compatibleProviders.flatMap((provider) => (credentials[provider.ID] || []).map((credential) => credential.id))),
    [compatibleProviders, credentials],
  );
  const unavailableSelectedCount = useMemo(
    () => value.filter((credentialID) => !compatibleCredentialIDs.has(credentialID)).length,
    [compatibleCredentialIDs, value],
  );

  const updateSelection = (next: Iterable<string>) => onChange(unique(next));
  const setProviderEnabled = (providerID: string, enabled: boolean) => {
    const providerCredentialIDs = (credentials[providerID] || []).map((credential) => credential.id);
    const next = new Set(value);
    if (enabled) {
      for (const credential of credentials[providerID] || []) {
        if (credential.enabled) next.add(credential.id);
      }
    } else {
      for (const credentialID of providerCredentialIDs) next.delete(credentialID);
    }
    updateSelection(next);
  };
  const setCredentialEnabled = (credential: ApiKeyCredential, enabled: boolean) => {
    if (enabled && !credential.enabled) return;
    const next = new Set(value);
    if (enabled) next.add(credential.id);
    else next.delete(credential.id);
    updateSelection(next);
  };

  return (
    <div className="api-credential-access">
      <div className="api-credential-access-intro">
        <div>
          <p className="api-model-access-title">Restrict account routing</p>
          <p className="api-model-access-hint">
            Optional. Choose only providers available through the selected PPM routes, then choose their accounts. Rotation and failover stay within these accounts.
          </p>
        </div>
        <Badge variant={value.length ? "info" : "default"} size="sm">
          {value.length ? `${value.length} account${value.length === 1 ? "" : "s"}` : "All accounts"}
        </Badge>
      </div>

      {compatibleProviders.length === 0 ? (
        <div className="api-credential-access-empty">
          Select at least one virtual model with an enabled PPM route before restricting accounts.
        </div>
      ) : (
        <div className="api-credential-access-list custom-scrollbar">
          {compatibleProviders.map((provider) => {
            const providerCredentials = credentials[provider.ID] || [];
            const providerSelected = providerCredentials.some((credential) => selectedCredentialIDs.has(credential.id));
            const hasEnabledCredential = providerCredentials.some((credential) => credential.enabled);
            return (
              <div key={provider.ID} className={`api-credential-provider${providerSelected ? " is-enabled" : ""}`}>
                <div className="api-credential-provider-head">
                  <div className="api-model-access-label">
                    <strong>{provider.Name || provider.ID}</strong>
                    {provider.Name !== provider.ID ? <code>{provider.ID}</code> : null}
                  </div>
                  <Toggle
                    label=""
                    checked={providerSelected}
                    disabled={!hasEnabledCredential && !providerSelected}
                    onChange={(event) => setProviderEnabled(provider.ID, event.target.checked)}
                    aria-label={`Allow ${provider.Name || provider.ID}`}
                  />
                </div>

                {providerSelected ? (
                  <div className="api-credential-account-list">
                    {providerCredentials.length === 0 ? (
                      <p className="api-credential-access-empty">No accounts configured for this provider.</p>
                    ) : (
                      providerCredentials.map((credential) => {
                        const selected = selectedCredentialIDs.has(credential.id);
                        return (
                          <div key={credential.id} className={`api-credential-account${selected ? " is-enabled" : ""}`}>
                            <div className="api-model-access-label">
                              <strong>{credential.label || credential.email || credential.id}</strong>
                              <code>{credential.email || credential.id}</code>
                            </div>
                            <div className="api-model-access-action">
                              {!credential.enabled ? <Badge variant="warning" size="sm">Disabled</Badge> : null}
                              <Toggle
                                label=""
                                checked={selected}
                                disabled={!credential.enabled && !selected}
                                onChange={(event) => setCredentialEnabled(credential, event.target.checked)}
                                aria-label={`Allow ${credential.label || credential.id}`}
                              />
                            </div>
                          </div>
                        );
                      })
                    )}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      )}

      {unavailableSelectedCount > 0 ? (
        <div className="api-credential-access-unavailable">
          <span>{unavailableSelectedCount} selected account{unavailableSelectedCount === 1 ? " is" : "s are"} no longer available for these models.</span>
          <Button type="button" variant="ghost" size="sm" onClick={() => updateSelection(value.filter((id) => compatibleCredentialIDs.has(id)))}>
            Remove unavailable
          </Button>
        </div>
      ) : null}
    </div>
  );
}
