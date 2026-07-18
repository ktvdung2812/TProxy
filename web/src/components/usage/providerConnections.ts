import { getProviderTypeInfo } from "../providers/catalog";
import type { Credential } from "../providers/types";

type ProviderRef = {
  id: string;
  type: string;
  enabled: boolean;
};

/** True when a provider has credentials or a no-auth connection configured. */
export function providerHasConnection(
  provider: ProviderRef,
  credentials: Record<string, Credential[]>,
): boolean {
  if ((credentials[provider.id] || []).length > 0) {
    return true;
  }
  return provider.enabled && getProviderTypeInfo(provider.type).noAuth === true;
}

export function filterConnectedProviders<T extends ProviderRef>(
  providers: T[],
  credentials: Record<string, Credential[]>,
): T[] {
  return providers.filter((provider) => providerHasConnection(provider, credentials));
}
