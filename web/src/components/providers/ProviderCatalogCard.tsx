import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { cn } from "../ui";
import type { ProviderTypeInfo } from "./catalog";
import { ProviderLogo } from "./ProviderLogo";
import { providerLogoId } from "./ProviderListItem";
import { countAccounts } from "./providerHealth";
import type { Credential, Provider } from "./types";

export type CatalogAuthKind = "oauth" | "apikey" | "cookie" | "serviceAccount" | "noAuth";

type Props = {
  catalog: ProviderTypeInfo;
  instances: Provider[];
  credentials: Record<string, Credential[]>;
  to: string;
  auth: CatalogAuthKind;
};

/**
 * Compact catalog tile. Providers that are already set up show a check and
 * their account count, so the catalog doubles as a shortcut to them.
 */
export function ProviderCatalogCard({ catalog, instances, credentials, to, auth }: Props) {
  const { t } = useTranslation();
  const counts = countAccounts(instances.flatMap((provider) => credentials[provider.ID] || []));
  const connected = instances.length > 0;
  const disabled = connected && instances.every((provider) => !provider.Enabled);
  return (
    <Link
      to={to}
      className={cn("pv-tile", connected && "is-connected", disabled && "is-off")}
      title={connected ? t("providers.catalog.openConnected", { name: catalog.name }) : t("providers.catalog.connectNamed", { name: catalog.name })}
    >
      <ProviderLogo
        className="pv-tile-logo"
        providerId={catalog.presetId}
        providerType={catalog.type}
        logoId={providerLogoId(catalog.presetId, catalog.type, Boolean(catalog.presetId))}
        style={{ color: catalog.color }}
      />
      <span className="pv-tile-copy">
        <strong>{catalog.name}</strong>
        <small>{t(`providers.catalog.auth.${auth}`)}</small>
      </span>
      {connected ? (
        <span className="pv-tile-state" aria-label={t("providers.catalog.connectedAccounts", { count: counts.total })}>
          <span className="material-symbols-outlined" aria-hidden>{disabled ? "pause_circle" : "check_circle"}</span>
          {counts.total > 0 ? counts.total : null}
        </span>
      ) : (
        <span className="material-symbols-outlined pv-tile-go" aria-hidden>add</span>
      )}
    </Link>
  );
}

/** Tile that starts the "add a custom OpenAI/Anthropic-compatible endpoint" form. */
export function CustomEndpointCard({ type, onAdd }: { type: "openai-compatible" | "anthropic-compatible"; onAdd: () => void }) {
  const { t } = useTranslation();
  return (
    <button type="button" className="pv-tile pv-tile-custom" onClick={onAdd}>
      <ProviderLogo className="pv-tile-logo" providerType={type} />
      <span className="pv-tile-copy">
        <strong>{t(type === "openai-compatible" ? "providers.providerListOpenAICompatible" : "providers.providerListAnthropicCompatible")}</strong>
        <small>{t(type === "openai-compatible" ? "providers.catalog.customOpenAIHint" : "providers.catalog.customAnthropicHint")}</small>
      </span>
      <span className="material-symbols-outlined pv-tile-go" aria-hidden>add</span>
    </button>
  );
}
