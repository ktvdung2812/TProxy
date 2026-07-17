import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { RequestDetailsTab } from "./RequestDetailsTab";
import { UsageStatsView } from "./UsageStatsView";
import { USAGE_PERIODS } from "./utils";
import type { UsagePeriod } from "./api";

type ProviderItem = {
  ID: string;
  Name: string;
  Type: string;
  Enabled: boolean;
};

type CredentialRecord = {
  id: string;
  label?: string;
  email?: string;
  enabled?: boolean;
};

type Props = {
  secret: string;
  providers: ProviderItem[] | null;
  credentials?: Record<string, CredentialRecord[]>;
  onError: (message: string) => void;
};

type UsageTab = "overview" | "details";

export function UsageView({ secret, providers, credentials, onError }: Props) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [period, setPeriod] = useState<UsagePeriod>("today");

  const tabParam = searchParams.get("tab");
  const activeTab: UsageTab = tabParam === "details" ? "details" : "overview";

  const providerItems = useMemo(
    () => (providers || []).map((provider) => ({
      id: provider.ID,
      name: provider.Name,
      type: provider.Type,
      enabled: provider.Enabled,
    })),
    [providers],
  );

  const providerNames = useMemo(() => {
    const names: Record<string, string> = {};
    for (const provider of providerItems) {
      names[provider.id] = provider.name || provider.id;
    }
    return names;
  }, [providerItems]);

  const credentialsByProvider = useMemo(() => {
    const mapped: Record<string, Array<{ id: string; label?: string; email?: string; enabled?: boolean }>> = {};
    for (const [providerId, items] of Object.entries(credentials || {})) {
      mapped[providerId] = items.map((item) => ({
        id: item.id,
        label: item.label,
        email: item.email,
        enabled: item.enabled,
      }));
    }
    return mapped;
  }, [credentials]);

  const setTab = (tab: UsageTab) => {
    const next = new URLSearchParams(searchParams);
    next.set("tab", tab);
    setSearchParams(next, { replace: true });
  };

  return (
    <section className="section usage-page">
      <div className="usage-page-toolbar">
        <div className="usage-segmented">
          <button type="button" className={activeTab === "overview" ? "active" : ""} onClick={() => setTab("overview")}>
            Overview
          </button>
          <button type="button" className={activeTab === "details" ? "active" : ""} onClick={() => setTab("details")}>
            Details
          </button>
        </div>
        {activeTab === "overview" ? (
          <div className="usage-segmented usage-periods">
            {USAGE_PERIODS.map((item) => (
              <button
                key={item.value}
                type="button"
                className={period === item.value ? "active" : ""}
                onClick={() => setPeriod(item.value as UsagePeriod)}
              >
                {item.label}
              </button>
            ))}
          </div>
        ) : null}
      </div>

      {activeTab === "overview" ? (
        <UsageStatsView
          secret={secret}
          period={period}
          providers={providerItems}
          credentialsByProvider={credentialsByProvider}
          onError={onError}
        />
      ) : (
        <RequestDetailsTab secret={secret} providers={providerItems} providerNames={providerNames} onError={onError} />
      )}
    </section>
  );
}
