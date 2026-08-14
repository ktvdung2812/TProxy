import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Input, Toggle } from "../ui";
import type { ApiModelOption } from "./types";

type Props = {
  value: string;
  modelOptions: ApiModelOption[];
  onChange: (value: string) => void;
};

function parseSelection(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function serializeSelection(values: Iterable<string>): string {
  return [...values].join(", ");
}

export function ApiModelAccessField({ value, modelOptions, onChange }: Props) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const configured = useMemo(() => parseSelection(value), [value]);
  const allModels = configured.includes("*");
  const configuredSet = useMemo(() => new Set(configured.filter((id) => id !== "*")), [configured]);

  const options = useMemo(() => {
    const known = new Set(modelOptions.map((option) => option.value));
    const unavailable: ApiModelOption[] = [...configuredSet]
      .filter((id) => !known.has(id))
      .map((id) => ({ value: id, label: id, group: "models" }));
    return [...modelOptions, ...unavailable];
  }, [configuredSet, modelOptions]);

  const filteredOptions = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return options;
    return options.filter((option) => `${option.label} ${option.value}`.toLowerCase().includes(query));
  }, [options, search]);

  const knownIDs = useMemo(() => new Set(modelOptions.map((option) => option.value)), [modelOptions]);
  const selectedCount = allModels ? modelOptions.length : configuredSet.size;
  const selectionTotal = allModels ? modelOptions.length : options.length;

  const setAllModels = (enabled: boolean) => {
    if (enabled) {
      onChange("*");
      return;
    }
    onChange(serializeSelection(modelOptions.map((option) => option.value)));
  };

  const setModelEnabled = (modelID: string, enabled: boolean) => {
    const next = allModels
      ? new Set(modelOptions.map((option) => option.value))
      : new Set(configuredSet);
    if (enabled) next.add(modelID);
    else next.delete(modelID);
    onChange(serializeSelection(next));
  };

  return (
    <div className="api-model-access">
      <div className="api-model-access-mode">
        <div>
          <p className="api-model-access-title">{t("apis.allModels")}</p>
          <p className="api-model-access-hint">{t("apis.allModelsHint")}</p>
        </div>
        <Toggle
          label=""
          checked={allModels}
          onChange={(event) => setAllModels(event.target.checked)}
          aria-label={t("apis.allModels")}
        />
      </div>

      <div className="api-model-access-toolbar">
        <Input
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t("apis.searchModels")}
          icon="search"
        />
        <span className="api-model-access-count">
          {allModels
            ? t("apis.allModelsSelected", { count: modelOptions.length })
            : t("apis.selectedModelsCount", { selected: selectedCount, total: selectionTotal })}
        </span>
      </div>

      {options.length === 0 ? (
        <div className="api-model-access-empty">
          <span className="material-symbols-outlined">view_in_ar</span>
          <p>{t("apis.noModelsConfigured")}</p>
        </div>
      ) : filteredOptions.length === 0 ? (
        <p className="api-model-access-empty-text">{t("apis.noModelsMatch")}</p>
      ) : (
        <div className="api-model-access-list custom-scrollbar">
          {filteredOptions.map((option) => {
            const available = knownIDs.has(option.value);
            const enabled = allModels || configuredSet.has(option.value);
            return (
              <div key={option.value} className={`api-model-access-row${enabled ? " is-enabled" : ""}`}>
                <div className="api-model-access-label">
                  <strong>{option.label}</strong>
                  {option.label !== option.value ? <code>{option.value}</code> : null}
                </div>
                <div className="api-model-access-action">
                  <Badge variant={available ? (option.group === "combos" ? "info" : "default") : "warning"} size="sm">
                    {available
                      ? t(option.group === "combos" ? "apis.comboModel" : "apis.virtualModel")
                      : t("apis.unavailableModel")}
                  </Badge>
                  <Toggle
                    label=""
                    checked={enabled}
                    onChange={(event) => setModelEnabled(option.value, event.target.checked)}
                    aria-label={t("apis.toggleModel", { model: option.label })}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}

      <div className="api-model-access-footer">
        <p>{allModels ? t("apis.allModelsAutomatic") : t("apis.customModelsHint")}</p>
        {!allModels ? (
          <div>
            <Button type="button" variant="ghost" size="sm" onClick={() => onChange("")}>
              {t("apis.disableAllModels")}
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => onChange("*")}>
              {t("apis.enableAllModels")}
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
