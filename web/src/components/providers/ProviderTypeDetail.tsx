import { useTranslation } from "react-i18next";
import { PageNote, SectionHead, StatusPill, Tag, ToolButton } from "../page-kit";
import type { ProviderTypeInfo } from "./catalog";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";
import { ProviderConnectionActions } from "./ProviderConnectionActions";
import { ProviderIdentity } from "./ProviderIdentity";
import { providerLogoId } from "./ProviderListItem";

type Props = {
  catalog: ProviderTypeInfo;
  connectionProfile: ConnectionProfile;
  onBack: () => void;
  onSetup: () => void;
  onConnectionMethod: (method: ConnectionMethod) => void;
  connectBusy?: boolean;
};

/** Detail page for a catalog provider with no configured instance yet: pick how to connect. */
export function ProviderTypeDetail({ catalog, connectionProfile, onBack, onSetup, onConnectionMethod, connectBusy }: Props) {
  const { t } = useTranslation();
  return (
    <section className="cp-page pv-detail">
      <ProviderIdentity
        name={catalog.name}
        code={catalog.presetId}
        providerId={catalog.presetId}
        providerType={catalog.type}
        logoId={providerLogoId(catalog.presetId, catalog.type, Boolean(catalog.presetId))}
        color={catalog.color}
        baseUrl={catalog.defaultBaseUrl}
        website={catalog.website}
        apiKeyUrl={catalog.apiKeyUrl}
        onBack={onBack}
        tags={
          <>
            <StatusPill tone="neutral">{t("providers.typeDetail.notConfigured")}</StatusPill>
            {catalog.type !== catalog.presetId ? <Tag tone="primary">{catalog.type}</Tag> : null}
          </>
        }
        description={catalog.description || null}
        actions={
          <ToolButton icon="tune" onClick={onSetup} title={t("providers.typeDetail.advancedHint")}>
            {t("providers.typeDetail.advanced")}
          </ToolButton>
        }
      />

      <section className="cp-panel pv-panel pv-connect-panel" aria-label={t("providers.connections")}>
        <SectionHead
          leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>vpn_key</span>}
          title={t("providers.account.connectTitle")}
          subtitle={connectionProfile.noAuth ? t("providers.noAuthHint") : t("providers.typeDetail.connectHint")}
        />
        <ProviderConnectionActions profile={connectionProfile} onMethod={onConnectionMethod} busy={connectBusy} placement="empty" size="md" />
        <PageNote icon="auto_awesome">{t("providers.typeDetail.autoCreate")}</PageNote>
      </section>
    </section>
  );
}
