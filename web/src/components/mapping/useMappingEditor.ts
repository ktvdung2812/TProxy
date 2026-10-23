import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  fetchClaudeMapping,
  fetchCursorMapping,
  fetchModelMapping,
  saveClaudeMapping,
  saveCursorMapping,
  saveModelMapping,
  testModelMapping,
  type ClaudeMappingResponse,
  type CursorMappingResponse,
  type ModelMappingResponse,
} from "./api";
import { MAPPING_TIERS, type MappingTier, type ReasoningEffort } from "./codegen";
import type { MappingClientTab } from "./utils";

/** Claude and Codex edit the same tier map; Cursor and global models each have their own. */
export type MappingSection = "tiers" | "cursor" | "models";
export type PairSection = Exclude<MappingSection, "tiers">;
/** How a row differs from what the server last returned. */
export type RowState = "saved" | "new" | "changed" | "incomplete";

export function sectionOf(tab: MappingClientTab): MappingSection {
  if (tab === "cursor") return "cursor";
  if (tab === "models") return "models";
  return "tiers";
}

function tierRecord<T extends string>(source: Partial<Record<string, T>> | undefined): Record<MappingTier, T | ""> {
  const next = {} as Record<MappingTier, T | "">;
  for (const tier of MAPPING_TIERS) next[tier] = source?.[tier] || "";
  return next;
}

/** Only keep non-empty mappings — users add pairs on demand. */
export function buildCursorOverrideState(overrides: Record<string, string> | undefined): Record<string, string> {
  const next: Record<string, string> = {};
  if (!overrides) return next;
  for (const [source, target] of Object.entries(overrides)) {
    const key = source.trim();
    const value = (target || "").trim();
    if (!key || !value) continue;
    next[key] = value;
  }
  return next;
}

export function rowState(saved: Record<string, string>, source: string, target: string): RowState {
  if (!target.trim()) return "incomplete";
  if (!(source in saved)) return "new";
  return saved[source] === target ? "saved" : "changed";
}

/** Sources added, retargeted or dropped relative to the saved map. */
function diffPairs(saved: Record<string, string>, current: Record<string, string>) {
  const removed = Object.keys(saved).filter((source) => !(source in current));
  const touched = Object.keys(current).filter((source) => saved[source] !== current[source]);
  return { removed, touched };
}

type Options = {
  secret: string;
  onError: (message: string) => void;
  onNotice: (message: string) => void;
};

export function useMappingEditor({ secret, onError, onNotice }: Options) {
  const { t } = useTranslation();
  const [data, setData] = useState<ClaudeMappingResponse | null>(null);
  const [cursorData, setCursorData] = useState<CursorMappingResponse | null>(null);
  const [modelData, setModelData] = useState<ModelMappingResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [refreshingCursorCatalog, setRefreshingCursorCatalog] = useState(false);

  const [overrides, setOverrides] = useState<Record<string, string>>(() => tierRecord<string>(undefined));
  const [efforts, setEfforts] = useState<Record<string, ReasoningEffort>>(() => tierRecord<ReasoningEffort>(undefined));
  const [cursorOverrides, setCursorOverrides] = useState<Record<string, string>>({});
  const [modelOverrides, setModelOverrides] = useState<Record<string, string>>({});
  const [addCursorSource, setAddCursorSource] = useState("");
  const [addCursorTarget, setAddCursorTarget] = useState("");
  const [addModelSource, setAddModelSource] = useState("");
  const [addModelTarget, setAddModelTarget] = useState("");

  const [resolveInput, setResolveInput] = useState("");
  const [resolveResult, setResolveResult] = useState<string[]>([]);
  const [resolving, setResolving] = useState(false);

  const applyClaudeResponse = useCallback((response: ClaudeMappingResponse) => {
    setData(response);
    setOverrides(tierRecord<string>(response.overrides));
    setEfforts(tierRecord<ReasoningEffort>(response.reasoning_effort_overrides));
  }, []);

  const applyCursorResponse = useCallback(
    (response: CursorMappingResponse, options?: { replaceOverrides?: boolean }) => {
      setCursorData(response);
      if (options?.replaceOverrides !== false) {
        setCursorOverrides(buildCursorOverrideState(response.overrides));
      }
    },
    [],
  );

  const applyModelResponse = useCallback((response: ModelMappingResponse) => {
    setModelData(response);
    setModelOverrides({ ...(response.overrides || {}) });
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [claudeResponse, cursorResponse, modelResponse] = await Promise.all([
        fetchClaudeMapping(secret),
        // Backend auto-upgrades from static→live; no need for ?refresh=1 on every page load.
        fetchCursorMapping(secret),
        fetchModelMapping(secret),
      ]);
      applyClaudeResponse(claudeResponse);
      applyCursorResponse(cursorResponse);
      applyModelResponse(modelResponse);
      setAddCursorSource("");
      setAddCursorTarget("");
      setAddModelSource("");
      setAddModelTarget("");

      // If we still only got the thin static fallback, force a live rediscover once.
      const catalogLen = cursorResponse.cursor_models?.length || cursorResponse.catalog_count || 0;
      if (catalogLen > 0 && catalogLen <= 20 && cursorResponse.catalog_source !== "discovery") {
        setRefreshingCursorCatalog(true);
        try {
          const live = await fetchCursorMapping(secret, { refresh: true });
          applyCursorResponse(live);
        } catch {
          /* keep static catalog */
        } finally {
          setRefreshingCursorCatalog(false);
        }
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToLoad"));
    } finally {
      setLoading(false);
    }
  }, [secret, onError, applyClaudeResponse, applyCursorResponse, applyModelResponse, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const refreshCursorCatalog = useCallback(async () => {
    setRefreshingCursorCatalog(true);
    try {
      const response = await fetchCursorMapping(secret, { refresh: true });
      // Keep unsaved local rows; only refresh the Cursor model catalog.
      applyCursorResponse(response, { replaceOverrides: false });
      const count = response.cursor_models?.length || response.catalog_count || 0;
      onNotice(
        count > 0
          ? t("mapping.loadedCursorModels", { count, source: response.catalog_source || "catalog" })
          : t("mapping.cursorCatalogRefreshed"),
      );
      if (response.discovery_error && count <= 20) {
        onError(response.discovery_error);
      }
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToRefreshCursor"));
    } finally {
      setRefreshingCursorCatalog(false);
    }
  }, [secret, applyCursorResponse, onNotice, onError, t]);

  /* ---------- saved baselines and unsaved state ---------- */
  const savedTiers = useMemo(() => tierRecord<string>(data?.overrides), [data]);
  const savedEfforts = useMemo(() => tierRecord<ReasoningEffort>(data?.reasoning_effort_overrides), [data]);
  const savedCursor = useMemo(() => buildCursorOverrideState(cursorData?.overrides), [cursorData]);
  const savedModels = useMemo(() => ({ ...(modelData?.overrides || {}) }), [modelData]);

  const changedTiers = useMemo(
    () => MAPPING_TIERS.filter((tier) => (overrides[tier] || "") !== savedTiers[tier] || (efforts[tier] || "") !== savedEfforts[tier]),
    [overrides, efforts, savedTiers, savedEfforts],
  );
  const cursorDiff = useMemo(() => diffPairs(savedCursor, cursorOverrides), [savedCursor, cursorOverrides]);
  const modelDiff = useMemo(() => diffPairs(savedModels, modelOverrides), [savedModels, modelOverrides]);
  // A filled-in Add row is saved along with the list, so it counts as a pending change.
  const cursorPending = addCursorSource.trim() && addCursorTarget.trim() ? 1 : 0;
  const modelPending = addModelSource.trim() && addModelTarget.trim() ? 1 : 0;

  const dirty: Record<MappingSection, number> = {
    tiers: changedTiers.length,
    cursor: cursorDiff.touched.length + cursorDiff.removed.length + cursorPending,
    models: modelDiff.touched.length + modelDiff.removed.length + modelPending,
  };
  const removed: Record<PairSection, string[]> = { cursor: cursorDiff.removed, models: modelDiff.removed };

  /* ---------- tier edits ---------- */
  const setTierTarget = (tier: string, value: string) => setOverrides((current) => ({ ...current, [tier]: value }));
  const setTierEffort = (tier: string, value: ReasoningEffort) => setEfforts((current) => ({ ...current, [tier]: value }));
  /** Clears the override so the tier falls back to its env/config default. */
  const resetTier = (tier: MappingTier) => {
    setTierTarget(tier, "");
    setTierEffort(tier, "");
  };
  const revertTier = (tier: MappingTier) => {
    setTierTarget(tier, savedTiers[tier]);
    setTierEffort(tier, savedEfforts[tier]);
  };

  /* ---------- pair edits (Cursor + global models) ---------- */
  const setPairs = (section: PairSection) => (section === "cursor" ? setCursorOverrides : setModelOverrides);
  const setPairTarget = (section: PairSection, sources: string[], target: string) =>
    setPairs(section)((current) => {
      const next = { ...current };
      for (const source of sources) next[source] = target;
      return next;
    });
  const removePairs = (section: PairSection, sources: string[]) =>
    setPairs(section)((current) => {
      const next = { ...current };
      for (const source of sources) delete next[source];
      return next;
    });
  const revertPair = (section: PairSection, source: string) => {
    const saved = section === "cursor" ? savedCursor : savedModels;
    setPairs(section)((current) => {
      const next = { ...current };
      if (source in saved) next[source] = saved[source];
      else delete next[source];
      return next;
    });
  };

  const addCursorMapping = () => {
    const source = addCursorSource.trim();
    const target = addCursorTarget.trim();
    if (!source || !target) {
      onError(t("mapping.selectBothForAdd"));
      return false;
    }
    if (cursorOverrides[source]?.trim()) {
      onError(t("mapping.cursorAlreadyMapped", { source }));
      return false;
    }
    setCursorOverrides((current) => ({ ...current, [source]: target }));
    setAddCursorSource("");
    setAddCursorTarget("");
    return true;
  };

  const addModelMapping = () => {
    const source = addModelSource.trim();
    const target = addModelTarget.trim();
    if (!source || !target) {
      onError(t("mapping.selectBothForAddModel"));
      return false;
    }
    if (source.toLowerCase() === target.toLowerCase()) {
      onError(t("mapping.modelIdentityRejected", { source }));
      return false;
    }
    setModelOverrides((current) => ({ ...current, [source]: target }));
    setAddModelSource("");
    setAddModelTarget("");
    return true;
  };

  /** A row whose target was cleared would otherwise be dropped silently on save. */
  const firstIncomplete = (pairs: Record<string, string>) =>
    Object.entries(pairs).find(([, target]) => !target.trim())?.[0];

  /* ---------- saves ---------- */
  const saveTiers = async () => {
    setSaving(true);
    try {
      const response = await saveClaudeMapping(secret, { overrides, reasoning_effort_overrides: efforts });
      applyClaudeResponse(response);
      onNotice(t("mapping.routingMapSaved"));
      return true;
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToSave"));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const saveCursor = async () => {
    setSaving(true);
    try {
      const incomplete = firstIncomplete(cursorOverrides);
      if (incomplete) throw new Error(t("mapping.rowNeedsTarget", { source: incomplete }));
      // Include the pending form row so Save works without a separate Add click.
      const merged: Record<string, string> = { ...cursorOverrides };
      const pendingSource = addCursorSource.trim();
      const pendingTarget = addCursorTarget.trim();
      if (pendingSource && pendingTarget) {
        merged[pendingSource] = pendingTarget;
      } else if (pendingSource || pendingTarget) {
        throw new Error(t("mapping.selectBothForSave"));
      }

      const payload = buildCursorOverrideState(merged);
      const previouslySaved = Object.keys(savedCursor).length > 0;
      if (Object.keys(payload).length === 0 && !previouslySaved) {
        throw new Error(t("mapping.addAtLeastOne"));
      }

      const response = await saveCursorMapping(secret, { overrides: payload });
      const saved = buildCursorOverrideState(response.overrides ?? payload);
      if (Object.keys(saved).length === 0 && Object.keys(payload).length > 0) {
        // Keep local rows if the server unexpectedly returned an empty map.
        setCursorOverrides(payload);
        throw new Error(t("mapping.serverReturnedNoMappings"));
      }
      setCursorData(response);
      setCursorOverrides(saved);
      setAddCursorSource("");
      setAddCursorTarget("");
      onNotice(
        Object.keys(saved).length === 0
          ? t("mapping.clearedAllCursorMappings")
          : t("mapping.savedCursorMappings", { count: Object.keys(saved).length }),
      );
      return true;
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToSaveCursor"));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const saveModels = async () => {
    setSaving(true);
    try {
      const incomplete = firstIncomplete(modelOverrides);
      if (incomplete) throw new Error(t("mapping.rowNeedsTarget", { source: incomplete }));
      const merged: Record<string, string> = { ...modelOverrides };
      const pendingSource = addModelSource.trim();
      const pendingTarget = addModelTarget.trim();
      if (pendingSource && pendingTarget) {
        merged[pendingSource] = pendingTarget;
      } else if (pendingSource || pendingTarget) {
        throw new Error(t("mapping.selectBothForSaveModel"));
      }
      for (const [source, target] of Object.entries(merged)) {
        if (source.trim().toLowerCase() === target.trim().toLowerCase()) {
          throw new Error(t("mapping.modelIdentityRejected", { source }));
        }
      }
      const response = await saveModelMapping(secret, { overrides: merged });
      applyModelResponse(response);
      setAddModelSource("");
      setAddModelTarget("");
      const count = Object.keys(response.overrides || {}).length;
      onNotice(count === 0 ? t("mapping.clearedAllModelMappings") : t("mapping.savedModelMappings", { count }));
      return true;
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToSave"));
      return false;
    } finally {
      setSaving(false);
    }
  };

  const save = (section: MappingSection) =>
    section === "tiers" ? saveTiers() : section === "cursor" ? saveCursor() : saveModels();

  const discard = (section: MappingSection) => {
    if (section === "tiers") {
      setOverrides({ ...savedTiers });
      setEfforts({ ...savedEfforts });
    } else if (section === "cursor") {
      setCursorOverrides({ ...savedCursor });
      setAddCursorSource("");
      setAddCursorTarget("");
    } else {
      setModelOverrides({ ...savedModels });
      setAddModelSource("");
      setAddModelTarget("");
    }
  };

  const runResolveTest = async (model = resolveInput) => {
    const value = model.trim();
    if (!value) return;
    setResolveInput(value);
    setResolving(true);
    try {
      const result = await testModelMapping(secret, value);
      setResolveResult(result.steps || [result.requested, result.resolved]);
    } catch (cause) {
      onError(cause instanceof Error ? cause.message : t("mapping.failedToLoad"));
    } finally {
      setResolving(false);
    }
  };

  return {
    data,
    cursorData,
    modelData,
    loading,
    saving,
    refreshingCursorCatalog,
    load,
    refreshCursorCatalog,
    tiers: { overrides, efforts, saved: savedTiers, savedEfforts, changed: changedTiers, setTarget: setTierTarget, setEffort: setTierEffort, reset: resetTier, revert: revertTier },
    cursor: {
      overrides: cursorOverrides,
      saved: savedCursor,
      addSource: addCursorSource,
      addTarget: addCursorTarget,
      setAddSource: setAddCursorSource,
      setAddTarget: setAddCursorTarget,
      add: addCursorMapping,
    },
    models: {
      overrides: modelOverrides,
      saved: savedModels,
      addSource: addModelSource,
      addTarget: addModelTarget,
      setAddSource: setAddModelSource,
      setAddTarget: setAddModelTarget,
      add: addModelMapping,
    },
    setPairTarget,
    removePairs,
    revertPair,
    dirty,
    removed,
    save,
    discard,
    resolve: { input: resolveInput, setInput: setResolveInput, result: resolveResult, running: resolving, run: runResolveTest },
  };
}

export type MappingEditor = ReturnType<typeof useMappingEditor>;
