import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { EmptyPanel, PageNote, Tag } from "../page-kit";
import { Modal, cn } from "../ui";
import type { ChatModelOption } from "./types";

export const VIRTUAL_GROUP = "Virtual models";
export const COMBO_GROUP = "Combos";

/** Group ids come from useChatModels; provider groups are already display names. */
export function useGroupLabel() {
  const { t } = useTranslation();
  return (group: string) => {
    if (group === VIRTUAL_GROUP) return t("chat.groupVirtual");
    if (group === COMBO_GROUP) return t("chat.groupCombos");
    return group;
  };
}

function groupOrder(group: string) {
  if (group === VIRTUAL_GROUP) return 0;
  if (group === COMBO_GROUP) return 1;
  return 2;
}

type Props = {
  open: boolean;
  onClose: () => void;
  models: ChatModelOption[];
  activeModelId: string;
  onSelect: (modelId: string) => void;
  loadingProviders: Array<{ id: string; label: string }>;
  providerError?: string;
};

export function ChatModelPicker({ open, onClose, models, activeModelId, onSelect, loadingProviders, providerError }: Props) {
  const { t } = useTranslation();
  const groupLabel = useGroupLabel();
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    setQuery("");
  }, [open]);

  const groups = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const byGroup = new Map<string, ChatModelOption[]>();
    for (const model of models) {
      if (needle) {
        const haystack = [model.name, model.id, model.requestModel, model.group].filter(Boolean).join(" ").toLowerCase();
        if (!haystack.includes(needle)) continue;
      }
      byGroup.set(model.group, [...(byGroup.get(model.group) || []), model]);
    }
    return Array.from(byGroup.entries())
      .map(([group, items]) => ({ group, items: [...items].sort((a, b) => a.name.localeCompare(b.name)) }))
      .sort((a, b) => groupOrder(a.group) - groupOrder(b.group) || a.group.localeCompare(b.group));
  }, [models, query]);
  const flat = useMemo(() => groups.flatMap((group) => group.items), [groups]);

  // Start on the active model so Enter keeps it; jump to the first hit while searching.
  useEffect(() => {
    if (!open) return;
    const index = query ? 0 : Math.max(0, flat.findIndex((model) => model.id === activeModelId));
    setCursor(index);
  }, [open, query, flat, activeModelId]);

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)?.scrollIntoView({ block: "nearest" });
  }, [cursor]);

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!flat.length) return;
      const step = event.key === "ArrowDown" ? 1 : -1;
      setCursor((value) => (value + step + flat.length) % flat.length);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const model = flat[cursor];
      if (model) onSelect(model.id);
    }
  };

  let index = -1;
  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      icon="smart_toy"
      title={t("chat.selectModel")}
      subtitle={
        loadingProviders.length > 0
          ? `${t("chat.nAvailable", { count: models.length })}${t("chat.discoveringMore")}`
          : t("chat.nAvailable", { count: models.length })
      }
      className="ch-picker-modal"
    >
      <div className="ch-picker">
        <label className="cp-search ch-picker-search">
          <span className="material-symbols-outlined" aria-hidden>search</span>
          <input
            type="search"
            autoFocus
            value={query}
            placeholder={t("chat.searchModels")}
            aria-label={t("chat.searchModels")}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onKeyDown}
          />
          <kbd className="cp-kbd">↑↓ ↵</kbd>
        </label>

        <div ref={listRef} className="ch-picker-list custom-scrollbar" role="listbox" aria-label={t("chat.selectModel")}>
          {flat.length === 0 && loadingProviders.length === 0 ? (
            models.length === 0 ? (
              <EmptyPanel
                compact
                icon="smart_toy"
                title={t("chat.noModelsTitle")}
                description={t("chat.noModelsAvailable")}
                action={
                  <div className="ch-inline-links">
                    <Link to="/providers" onClick={onClose}>{t("chat.openProviders")}</Link>
                    <Link to="/models" onClick={onClose}>{t("chat.openModels")}</Link>
                  </div>
                }
              />
            ) : (
              <EmptyPanel compact icon="search_off" title={t("chat.noModelsMatchSearch")} />
            )
          ) : null}

          {groups.map((group) => (
            <section key={group.group} className="ch-picker-group">
              <header>
                <span>{groupLabel(group.group)}</span>
                <span className="cp-tab-count">{group.items.length}</span>
              </header>
              <div className="ch-picker-grid">
                {group.items.map((model) => {
                  index += 1;
                  const current = index;
                  const active = model.id === activeModelId;
                  return (
                    <button
                      key={model.id}
                      type="button"
                      role="option"
                      aria-selected={active}
                      data-index={current}
                      className={cn("ch-picker-option", active && "is-active", cursor === current && "is-cursor")}
                      onMouseEnter={() => setCursor(current)}
                      onClick={() => onSelect(model.id)}
                    >
                      <span className="ch-picker-option-name">
                        {model.name}
                        {active ? <span className="material-symbols-outlined" aria-hidden>check_circle</span> : null}
                      </span>
                      <span className="ch-picker-option-id">{model.requestModel || model.id}</span>
                      {model.capabilities.includes("vision") ? (
                        <span className="ch-picker-option-tags"><Tag tone="info">{t("chat.vision")}</Tag></span>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            </section>
          ))}

          {loadingProviders.map((provider) => (
            <section key={`loading-${provider.id}`} className="ch-picker-group is-loading">
              <header>
                <span>{provider.label}</span>
                <span className="material-symbols-outlined animate-spin" aria-hidden>progress_activity</span>
              </header>
              <p className="ch-picker-loading">{t("chat.discoveringModels")}</p>
            </section>
          ))}

          {providerError ? <PageNote icon="error">{t("chat.discoveryFailed")}</PageNote> : null}
        </div>
      </div>
    </Modal>
  );
}
