import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  LegendItem,
  LinkButton,
  PageGuide,
  PageNote,
  PageStats,
  SearchField,
  SectionHead,
  Segmented,
  SelectField,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  ToneCounts,
  usePageGuide,
  useStoredState,
} from "../page-kit";
import { Button } from "../ui";
import { fetchFreeTiers, type FreeTierItem } from "./api";
import { AUTH_ICONS, FreeTierCard, FreeTierListHead, FreeTierRow } from "./FreeTierItem";

type Props = {
  secret: string;
  onError: (message: string) => void;
};

type StatusFilter = "all" | "configured" | "available";
type SortKey = "status" | "name" | "limit";
type ViewMode = "grid" | "list";

const SORTS: SortKey[] = ["status", "name", "limit"];

export function FreeTiersView({ secret, onError }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const guide = usePageGuide("free-tiers");
  const [items, setItems] = useState<FreeTierItem[]>([]);
  const [strategies, setStrategies] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [query, setQuery] = useState("");
  const [authFilter, setAuthFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sort, setSort] = useStoredState<SortKey>("tproxy.freeTiers.sort", "status", SORTS);
  const [view, setView] = useStoredState<ViewMode>("tproxy.freeTiers.view", "grid", ["grid", "list"]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchFreeTiers(secret);
      setItems(data.items || []);
      setStrategies(data.strategies || []);
      setLoadError("");
    } catch (error) {
      const message = error instanceof Error ? error.message : t("freeTiers.failedLoad");
      setLoadError(message);
      onError(message);
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const authTypes = useMemo(() => {
    const counts = new Map<string, number>();
    for (const item of items) counts.set(item.auth_type, (counts.get(item.auth_type) || 0) + 1);
    return [...counts.entries()].sort((a, b) => b[1] - a[1]);
  }, [items]);

  const activeAuth = authFilter !== "all" && authTypes.some(([type]) => type === authFilter) ? authFilter : "all";
  const needle = query.trim().toLowerCase();
  const scoped = items.filter((item) => activeAuth === "all" || item.auth_type === activeAuth);
  const visible = scoped
    .filter((item) => statusFilter === "all" || (statusFilter === "configured") === item.configured)
    .filter((item) => {
      if (!needle) return true;
      return [item.name, item.provider_id, item.notes, item.daily_limit, ...(item.models || [])].some((value) => (value || "").toLowerCase().includes(needle));
    })
    .sort((a, b) => {
      if (sort === "status" && a.configured !== b.configured) return a.configured ? -1 : 1;
      if (sort === "limit" && Boolean(a.daily_limit) !== Boolean(b.daily_limit)) return a.daily_limit ? -1 : 1;
      return a.name.localeCompare(b.name);
    });

  const configured = visible.filter((item) => item.configured);
  const available = visible.filter((item) => !item.configured);
  const totals = {
    all: items.length,
    configured: items.filter((item) => item.configured).length,
    noKey: items.filter((item) => item.auth_type === "none").length,
    limits: items.filter((item) => item.daily_limit).length,
  };

  const hasFilters = needle !== "" || activeAuth !== "all" || statusFilter !== "all";
  const clearFilters = () => {
    setQuery("");
    setAuthFilter("all");
    setStatusFilter("all");
  };
  const toggleStatus = (value: StatusFilter) => setStatusFilter((current) => (current === value ? "all" : value));
  const openProvider = (item: FreeTierItem) => navigate(`/providers/${encodeURIComponent(item.provider_id)}`);

  const renderGroup = (key: "configured" | "available", list: FreeTierItem[]) => {
    if (!list.length) return null;
    return (
      <section key={key} className="ft-group" aria-label={t(`freeTiers.group.${key}`)}>
        <SectionHead
          leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>{key === "configured" ? "link" : "add_link"}</span>}
          title={t(`freeTiers.group.${key}`)}
          subtitle={t(`freeTiers.group.${key}Subtitle`, { count: list.length })}
          meta={
            key === "configured" ? (
              <ToneCounts items={[{ tone: "success", count: list.length, label: t("freeTiers.status.configured") }]} />
            ) : null
          }
        />
        {view === "grid" ? (
          <div className="cp-grid ft-grid">
            {list.map((item) => (
              <FreeTierCard key={item.provider_id} item={item} onOpen={() => openProvider(item)} />
            ))}
          </div>
        ) : (
          <div className="cp-list ft-list">
            <FreeTierListHead />
            {list.map((item) => (
              <FreeTierRow key={item.provider_id} item={item} onOpen={() => openProvider(item)} />
            ))}
          </div>
        )}
      </section>
    );
  };

  if (loading && !items.length && !loadError) {
    return (
      <section className="cp-page ft-page">
        <EmptyPanel loading title={t("common.loading")} />
      </section>
    );
  }

  return (
    <section className="cp-page ft-page">
      <PageStats
        label={t("freeTiers.stats.label")}
        items={[
          {
            key: "all",
            icon: "redeem",
            label: t("freeTiers.stats.all"),
            value: totals.all,
            hint: t("freeTiers.stats.allHint", { count: totals.limits }),
            active: !hasFilters,
            onClick: clearFilters,
            title: t("freeTiers.stats.allTitle"),
          },
          {
            key: "configured",
            icon: "link",
            tone: "success",
            label: t("freeTiers.stats.configured"),
            value: totals.configured,
            hint: t("freeTiers.stats.configuredHint"),
            muted: totals.configured === 0,
            active: statusFilter === "configured",
            onClick: () => toggleStatus("configured"),
            title: t("freeTiers.stats.filterHint"),
          },
          {
            key: "available",
            icon: "add_link",
            label: t("freeTiers.stats.available"),
            value: totals.all - totals.configured,
            hint: t("freeTiers.stats.availableHint"),
            muted: totals.all - totals.configured === 0,
            active: statusFilter === "available",
            onClick: () => toggleStatus("available"),
            title: t("freeTiers.stats.filterHint"),
          },
          {
            key: "nokey",
            icon: "key_off",
            label: t("freeTiers.stats.noKey"),
            value: totals.noKey,
            hint: t("freeTiers.stats.noKeyHint"),
            muted: totals.noKey === 0,
            active: activeAuth === "none",
            onClick: () => setAuthFilter((current) => (current === "none" ? "all" : "none")),
            title: t("freeTiers.stats.filterHint"),
          },
        ]}
      />

      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("freeTiers.filterByAccess")}
            value={activeAuth}
            onChange={(value) => setAuthFilter(value === activeAuth ? "all" : value)}
            items={[
              { value: "all", icon: "apps", label: t("freeTiers.allAccess"), count: items.length },
              ...authTypes.map(([type, count]) => ({
                value: type,
                icon: AUTH_ICONS[type] || "vpn_key",
                label: t(`freeTiers.auth.${type}`, { defaultValue: type }),
                count,
              })),
            ]}
          />
          <ToolbarEnd>
            <ToolButton icon="refresh" spinning={loading} disabled={loading} onClick={() => void load()}>
              {t("common.refresh")}
            </ToolButton>
            <Button variant="primary" size="sm" icon="add_link" onClick={() => navigate("/providers")}>
              {t("freeTiers.openProviders")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={query} onChange={setQuery} placeholder={t("freeTiers.search")} />
          <ToolbarEnd>
            <SelectField
              label={t("freeTiers.sortBy")}
              value={sort}
              onChange={setSort}
              options={SORTS.map((value) => ({ value, label: t(`freeTiers.sort.${value}`) }))}
            />
            <Segmented
              label={t("freeTiers.viewMode")}
              value={view}
              onChange={setView}
              items={[
                { value: "grid", icon: "grid_view", label: t("freeTiers.viewGrid") },
                { value: "list", icon: "view_list", label: t("freeTiers.viewList") },
              ]}
            />
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("freeTiers.guide.title")}
        steps={[
          { icon: "redeem", text: t("freeTiers.guide.what") },
          { icon: "add_link", text: t("freeTiers.guide.connect") },
          { icon: "layers", text: t("freeTiers.guide.combine") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("freeTiers.status.configured")}</LegendItem>
            <LegendItem tone="neutral">{t("freeTiers.status.available")}</LegendItem>
          </>
        }
      />

      {loadError && !items.length ? (
        <EmptyPanel
          icon="cloud_off"
          title={t("freeTiers.failedLoad")}
          description={<span role="alert">{loadError}</span>}
          action={<Button variant="secondary" icon="refresh" onClick={() => void load()}>{t("usage.retry")}</Button>}
        />
      ) : visible.length === 0 ? (
        hasFilters ? (
          <EmptyPanel
            icon="filter_alt_off"
            title={t("freeTiers.noMatch")}
            action={<LinkButton onClick={clearFilters}>{t("usage.clearFilters")}</LinkButton>}
          />
        ) : (
          <EmptyPanel icon="redeem" title={t("freeTiers.empty")} description={t("freeTiers.emptyHint")} />
        )
      ) : (
        <div className="ft-groups">
          {renderGroup("configured", configured)}
          {renderGroup("available", available)}
        </div>
      )}

      {strategies.length ? (
        <PageNote
          icon="layers"
          action={<LinkButton onClick={() => navigate("/combos")}>{t("freeTiers.openCombos")}</LinkButton>}
        >
          {t("freeTiers.strategiesNote", { strategies: strategies.join(", ") })}
        </PageNote>
      ) : null}
    </section>
  );
}
