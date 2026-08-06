import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Modal } from "../ui";
import { ADDABLE_PROVIDERS, getProviderTypeInfo, type ProviderTypeInfo } from "./catalog";
import { fetchNinerouterPresets, saveProvider, type NinerouterPreset } from "./api";
import { ProviderLogo } from "./ProviderLogo";

type Props = {
  open: boolean;
  secret: string;
  /** When set, skip the gallery and open the form for this provider type. */
  presetType?: string;
  onClose: () => void;
  onSaved?: (providerId: string, providerType: string) => void;
};

/**
 * Add a provider in two steps: (1) pick a type from the gallery,
 * (2) fill id/name/base_url. Ported in spirit from 9router's gallery UX,
 * but uses tdproxy types and the /api/admin/providers endpoint.
 */
export function AddProviderModal({ open, secret, presetType, onClose, onSaved }: Props) {
  const { t } = useTranslation();
  const [step, setStep] = useState<"pick" | "form">("pick");
  const [selected, setSelected] = useState<ProviderTypeInfo | null>(null);
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [presets, setPresets] = useState<NinerouterPreset[]>([]);
  const [presetFilter, setPresetFilter] = useState("");

  useEffect(() => {
    if (!secret) return;
    void fetchNinerouterPresets(secret)
      .then((result) => setPresets(result.presets || []))
      .catch(() => setPresets([]));
  }, [secret]);

  useEffect(() => {
    if (!open) return;
    setError("");
    if (presetType) {
      const preset = presets.find((item) => item.id === presetType);
      if (preset) {
        const info = getProviderTypeInfo(preset.type);
        setSelected(info);
        setId(preset.id);
        setName(preset.name);
        setBaseUrl(preset.base_url || info.defaultBaseUrl || "");
        setStep("form");
        return;
      }
      const info = getProviderTypeInfo(presetType);
      setSelected(info);
      setId(`${info.type}-${Date.now().toString(36).slice(-4)}`);
      setName(info.name);
      setBaseUrl(info.defaultBaseUrl || "");
      setStep("form");
      return;
    }
    setStep("pick");
    setSelected(null);
    setId("");
    setName("");
    setBaseUrl("");
  }, [open, presetType, presets]);

  const filteredPresets = useMemo(() => {
    const q = presetFilter.trim().toLowerCase();
    if (!q) return presets.slice(0, 24);
    return presets.filter((item) => item.id.includes(q) || item.name.toLowerCase().includes(q)).slice(0, 24);
  }, [presetFilter, presets]);

  const handlePickPreset = (preset: NinerouterPreset) => {
    const info = getProviderTypeInfo(preset.type);
    setSelected(info);
    setId(preset.id);
    setName(preset.name);
    setBaseUrl(preset.base_url || info.defaultBaseUrl || "");
    setStep("form");
  };

  const handlePick = (info: ProviderTypeInfo) => {
    setSelected(info);
    // Pre-fill sensible defaults from the catalog.
    setId(`${info.type}-${Date.now().toString(36).slice(-4)}`);
    setName(info.name);
    setBaseUrl(info.defaultBaseUrl || "");
    setStep("form");
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selected) return;
    setSaving(true);
    setError("");
    try {
      await saveProvider(secret, {
        id,
        type: selected.type,
        name,
        base_url: baseUrl || undefined,
        enabled: true,
      });
      onSaved?.(id, selected.type);
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("providers.failedSaveProvider"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={step === "pick" ? t("providers.addProvider") : `Add ${selected?.name ?? "provider"}`}
      subtitle={step === "pick" ? t("providers.chooseUpstreamType") : t("providers.setProviderIdentity")}
      icon={step === "pick" ? "add" : selected?.icon}
      size="md"
    >
      {step === "pick" ? (
        <>
          <div className="provider-gallery">
            {ADDABLE_PROVIDERS.map((info) => {
              const TypeInfo = getProviderTypeInfo(info.type);
              return (
                <button key={info.type} type="button" className="gallery-item" onClick={() => handlePick(TypeInfo)}>
                  <ProviderLogo
                    className="provider-logo"
                    providerType={info.type}
                    style={{ color: info.color }}
                  />
                  <span className="gallery-item-name">{info.name}</span>
                  <span className="gallery-item-desc">{info.description}</span>
                </button>
              );
            })}
          </div>
          {presets.length > 0 ? (
            <div style={{ marginTop: 16, display: "grid", gap: 8 }}>
              <Field label={t("providers.9routerPresets")} hint={t("providers.9routerPresetsHint")}>
                <Input
                  placeholder={t("providers.filterPresets")}
                  value={presetFilter}
                  onChange={(e) => setPresetFilter(e.target.value)}
                />
              </Field>
              <div className="provider-gallery" style={{ maxHeight: 220, overflow: "auto" }}>
                {filteredPresets.map((preset) => (
                  <button key={preset.id} type="button" className="gallery-item" onClick={() => handlePickPreset(preset)}>
                    <ProviderLogo className="provider-logo" providerType={preset.type} />
                    <span className="gallery-item-name">{preset.name}</span>
                    <span className="gallery-item-desc">{preset.id} · {preset.type}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
        </>
      ) : (
        <form onSubmit={handleSave} style={{ display: "grid", gap: 12 }}>
          <Field label="Type">
            <Input value={selected?.name ?? ""} disabled />
          </Field>
          <Field label="Provider ID" hint="Stable identifier used in routes." required>
            <Input placeholder="e.g. openai-main" value={id} onChange={(e) => setId(e.target.value)} required />
          </Field>
          <Field label="Display name">
            <Input placeholder="e.g. OpenAI primary" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Base URL" hint={selected?.defaultBaseUrl ? `Default: ${selected.defaultBaseUrl}` : "Optional"}>
            <Input placeholder="https://api.example.com" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          </Field>
          {error && (
            <p style={{ margin: 0, fontSize: 13, color: "var(--color-danger)", display: "flex", alignItems: "center", gap: 6 }}>
              <span className="material-symbols-outlined" style={{ fontSize: 16 }}>error</span>
              {error}
            </p>
          )}
          <div style={{ display: "flex", justifyContent: "space-between", gap: 8, marginTop: 4 }}>
            <Button type="button" variant="ghost" onClick={() => setStep("pick")} disabled={saving}>
              ← Back
            </Button>
            <div style={{ display: "flex", gap: 8 }}>
              <Button type="button" variant="secondary" onClick={onClose} disabled={saving}>
                Cancel
              </Button>
              <Button type="submit" variant="primary" icon="save" loading={saving}>
                Create provider
              </Button>
            </div>
          </div>
        </form>
      )}
    </Modal>
  );
}
