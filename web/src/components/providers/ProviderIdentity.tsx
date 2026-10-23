import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { LinkButton } from "../page-kit";
import { ProviderLogo } from "./ProviderLogo";

type Props = {
  name: string;
  providerId?: string;
  providerType: string;
  logoId?: string;
  color?: string;
  /** Shown next to the name when it differs, e.g. the provider instance id. */
  code?: string;
  tags?: ReactNode;
  description?: ReactNode;
  baseUrl?: string;
  website?: string;
  apiKeyUrl?: string;
  actions?: ReactNode;
  onBack: () => void;
};

/**
 * Who this provider is: logo, name, state and where it points. The app header
 * already says "Providers", so this is the entity's identity, not a page title.
 */
export function ProviderIdentity({ name, providerId, providerType, logoId, color, code, tags, description, baseUrl, website, apiKeyUrl, actions, onBack }: Props) {
  const { t } = useTranslation();
  return (
    <div className="cp-panel pv-identity">
      <LinkButton className="pv-back" onClick={onBack}>
        <span className="material-symbols-outlined" aria-hidden>arrow_back</span>
        {t("providers.backToProviders")}
      </LinkButton>
      <div className="pv-identity-main">
        <ProviderLogo
          className="pv-identity-logo"
          providerId={providerId}
          providerType={providerType}
          logoId={logoId}
          style={{ color }}
          alt={t("providers.identity.logoAlt", { name })}
        />
        <div className="pv-identity-copy">
          <div className="pv-identity-title">
            <strong>{name}</strong>
            {code && code !== name ? <code>{code}</code> : null}
          </div>
          {tags ? <div className="cp-card-tags">{tags}</div> : null}
          {description ? <p className="pv-identity-desc">{description}</p> : null}
          {baseUrl || website || apiKeyUrl ? (
            <div className="pv-identity-links">
              {baseUrl ? (
                <span className="pv-identity-url" title={baseUrl}>
                  <span className="material-symbols-outlined" aria-hidden>link</span>
                  {baseUrl}
                </span>
              ) : null}
              {website ? (
                <a href={website} target="_blank" rel="noreferrer">
                  {t("providers.website")}
                  <span className="material-symbols-outlined" aria-hidden>open_in_new</span>
                </a>
              ) : null}
              {apiKeyUrl ? (
                <a href={apiKeyUrl} target="_blank" rel="noreferrer">
                  {t("providers.getApiKey")}
                  <span className="material-symbols-outlined" aria-hidden>key</span>
                </a>
              ) : null}
            </div>
          ) : null}
        </div>
        {actions ? <div className="pv-identity-actions">{actions}</div> : null}
      </div>
    </div>
  );
}
