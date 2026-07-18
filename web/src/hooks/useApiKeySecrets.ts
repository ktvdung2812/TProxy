import { useSyncExternalStore } from "react";
import { getApiKeySecretsVersion, subscribeApiKeySecrets } from "../lib/apiKeySecrets";

/** Re-render when browser-stored API key secrets change. */
export function useApiKeySecrets(): void {
  useSyncExternalStore(subscribeApiKeySecrets, getApiKeySecretsVersion, () => 0);
}
