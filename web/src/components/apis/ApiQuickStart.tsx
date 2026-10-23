import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiKeySelect } from "../cli-tools/ApiKeySelect";
import { Field, Select } from "../ui";
import { IconAction, SectionHead, Tag, ToolButton } from "../page-kit";
import type { ApiKeyRecord, ApiModelOption } from "./types";
import { PROXY_ENDPOINTS, routeUrl } from "./utils";

type Props = {
  apiKeys: ApiKeyRecord[];
  modelOptions: ApiModelOption[];
  baseUrl: string;
  curlExample: string;
  exampleApiKey: string;
  onExampleApiKeyChange: (secret: string) => void;
  onExampleApiKeyIdChange: (id: string) => void;
  exampleModel: string;
  onExampleModelChange: (model: string) => void;
  copied: string | null;
  onCopy: (text: string, id: string) => void;
};

/** A ready-to-paste request for a chosen key and model, plus every route the gateway serves. */
export function ApiQuickStart({
  apiKeys,
  modelOptions,
  baseUrl,
  curlExample,
  exampleApiKey,
  onExampleApiKeyChange,
  onExampleApiKeyIdChange,
  exampleModel,
  onExampleModelChange,
  copied,
  onCopy,
}: Props) {
  const { t } = useTranslation();
  const [showRoutes, setShowRoutes] = useState(false);

  return (
    <section className="ap-section" id="ap-quick-start" aria-labelledby="ap-quick-start-title">
      <SectionHead
        leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>terminal</span>}
        title={<span id="ap-quick-start-title">{t("apis.quickStart.title")}</span>}
        subtitle={t("apis.quickStart.subtitle")}
      />
      <div className="cp-panel ap-quick">
        <div className="ap-quick-fields">
          <Field label={t("apis.exampleApiKey")}>
            <ApiKeySelect
              embedded
              apiKeys={apiKeys}
              value={exampleApiKey}
              onChange={onExampleApiKeyChange}
              onSelectedIdChange={onExampleApiKeyIdChange}
              emptyMessage={t("apis.noKeysForExample")}
              missingSecretMessage={t("apis.secretNotSavedForExample")}
            />
          </Field>
          <Field label={t("apis.exampleModel")} hint={t("apis.exampleModelHint")}>
            <Select value={exampleModel} onChange={(event) => onExampleModelChange(event.target.value)}>
              {modelOptions.length === 0 ? (
                <option value="">{t("apis.noModels")}</option>
              ) : (
                modelOptions.map((model) => (
                  <option key={model.value} value={model.value}>
                    {model.label}
                  </option>
                ))
              )}
            </Select>
          </Field>
        </div>

        <div className="ap-code">
          <div className="ap-code-head">
            <span>{t("apis.exampleRequest")}</span>
            <ToolButton icon={copied === "curl" ? "check" : "content_copy"} onClick={() => onCopy(curlExample, "curl")}>
              {copied === "curl" ? t("common.copied") : t("apis.copyCurl")}
            </ToolButton>
          </div>
          <pre className="custom-scrollbar">{curlExample}</pre>
        </div>

        <button type="button" className="ap-advanced-toggle ap-routes-toggle" aria-expanded={showRoutes} onClick={() => setShowRoutes((current) => !current)}>
          <span className="material-symbols-outlined" aria-hidden>{showRoutes ? "expand_less" : "expand_more"}</span>
          {t("apis.supportedRoutes")}
          <small>{t("apis.quickStart.routesCount", { count: PROXY_ENDPOINTS.length })}</small>
        </button>

        {showRoutes ? (
          <div className="cp-list ap-route-list">
            <div className="cp-list-head" aria-hidden>
              <span>{t("apis.quickStart.route")}</span>
              <span>{t("apis.quickStart.methods")}</span>
              <span>{t("apis.quickStart.description")}</span>
              <span />
            </div>
            {PROXY_ENDPOINTS.map((endpoint) => {
              const copyId = `route_${endpoint.id}`;
              return (
                <div className="cp-list-row ap-route-row" key={endpoint.path}>
                  <span className="cp-cell ap-route-path">
                    <code>{endpoint.path}</code>
                    {endpoint.capability ? <Tag tone="neutral">{endpoint.capability}</Tag> : null}
                  </span>
                  <span className="cp-cell ap-route-methods">
                    {endpoint.methods.map((method) => (
                      <Tag key={method} tone="info">{method}</Tag>
                    ))}
                  </span>
                  <span className="cp-cell ap-route-desc">{t(`apis.routeDesc.${endpoint.id}`, { defaultValue: endpoint.description })}</span>
                  <span className="cp-cell cp-cell-actions">
                    {endpoint.path.includes("*") || endpoint.path.includes("{") ? null : (
                      <IconAction
                        icon={copied === copyId ? "check" : "content_copy"}
                        label={copied === copyId ? t("common.copied") : t("apis.quickStart.copyRoute")}
                        onClick={() => onCopy(routeUrl(baseUrl, endpoint.path), copyId)}
                      />
                    )}
                  </span>
                </div>
              );
            })}
          </div>
        ) : null}
      </div>
    </section>
  );
}
