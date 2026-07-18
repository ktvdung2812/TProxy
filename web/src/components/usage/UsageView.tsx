import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import type { Credential } from "../providers/types";
import { RequestDetailsTab } from "./RequestDetailsTab";
import { UsageStatsView } from "./UsageStatsView";
import { filterConnectedProviders } from "./providerConnections";
import { USAGE_PERIODS } from "./utils";
import type { UsagePeriod } from "./api";

type ProviderItem = {
  ID: string;
  Name: string;
  Type: string;
  Enabled: boolean;
};

type Props = {
  secret: string;
  providers: ProviderItem[] | null;
  credentials?: Record<string, Credential[]>;
  onError: (message: string) => void;
};

type UsageTab = "overview" | "details";

export function UsageView({ secret, providers, credentials = {}, onError }: Props) {
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

  const connectedProviders = useMemo(
    () => filterConnectedProviders(providerItems, credentials),
    [providerItems, credentials],
  );

  const providerNames = useMemo(() => {
    const names: Record<string, string> = {};
    for (const provider of providerItems) {
      names[provider.id] = provider.name || provider.id;
    }
    return names;
  }, [providerItems]);

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
          providers={connectedProviders}
          onError={onError}
        />
      ) : (
        <RequestDetailsTab secret={secret} providers={providerItems} providerNames={providerNames} onError={onError} />
      )}
    </section>
  );
}
