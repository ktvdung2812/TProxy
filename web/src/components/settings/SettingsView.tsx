import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { GuideToggle, LegendItem, PageGuide, isDialogOpen, usePageGuide } from "../page-kit";
import {
  fetchAccountRotation,
  resetAccountRotationState,
  saveAccountRotation,
  type AccountRotationSettings,
} from "../providers/api";
import { Button, ConfirmDialog, cn } from "../ui";
import { AccessPanel } from "./AccessPanel";
import { fetchAdminSettings, saveGatewaySettings, type AdminSettings } from "./api";
import { BackupPanel } from "./BackupPanel";
import { PasswordPanel } from "./PasswordPanel";
import { FeaturesPanel, RetentionPanel } from "./ReadOnlyPanels";
import { RotationPanel } from "./RotationPanel";
import { modKeyLabel } from "../chat/utils";
import { SETTINGS_SECTIONS, type SettingsSectionId } from "./utils";

type Props = {
  secret: string;
  providers?: Array<{ ID: string; Name?: string }>;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
  onMutated?: () => void;
  onPasswordChanged?: (newPassword: string) => void;
};

const DEFAULT_STRATEGY = "round-robin";
const DEFAULT_STICKY = 3;

export function SettingsView({ secret, providers = [], onError, onNotice, onMutated, onPasswordChanged }: Props) {
  const { t } = useTranslation();
  const guide = usePageGuide("settings");
  const [loading, setLoading] = useState(true);
  const [settings, setSettings] = useState<AdminSettings | null>(null);
  const [rotation, setRotation] = useState<AccountRotationSettings | null>(null);

  // Drafts for the page-wide save bar; the saved values live in settings/rotation.
  const [strategy, setStrategy] = useState(DEFAULT_STRATEGY);
  const [stickyLimit, setStickyLimit] = useState(String(DEFAULT_STICKY));
  const [allowLan, setAllowLan] = useState(false);
  const [publicBaseUrl, setPublicBaseUrl] = useState("");
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [confirmReset, setConfirmReset] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [activeSection, setActiveSection] = useState<SettingsSectionId>("rotation");

  const applyLoaded = useCallback((adminSettings: AdminSettings, rotationSettings: AccountRotationSettings) => {
    setSettings(adminSettings);
    setRotation(rotationSettings);
    setStrategy(rotationSettings.strategy || DEFAULT_STRATEGY);
    setStickyLimit(String(rotationSettings.sticky_round_robin_limit || DEFAULT_STICKY));
    setAllowLan(Boolean(adminSettings.allow_lan_management));
    setPublicBaseUrl(adminSettings.public_base_url || "");
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [adminSettings, rotationSettings] = await Promise.all([
        fetchAdminSettings(secret),
        fetchAccountRotation(secret),
      ]);
      applyLoaded(adminSettings, rotationSettings);
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [onError, secret, applyLoaded]);

  useEffect(() => {
    void load();
  }, [load]);

  const savedStrategy = rotation?.strategy || DEFAULT_STRATEGY;
  const savedSticky = rotation?.sticky_round_robin_limit || DEFAULT_STICKY;
  const stickyNumber = Number(stickyLimit);
  const stickyError =
    !Number.isFinite(stickyNumber) || stickyNumber < 1 || !Number.isInteger(stickyNumber)
      ? t("settings.stickyLimitMin")
      : undefined;

  const dirty = useMemo(() => {
    const rotationDirty = Boolean(rotation) && (strategy !== savedStrategy || stickyLimit.trim() !== String(savedSticky));
    const lanDirty = Boolean(settings) && allowLan !== Boolean(settings?.allow_lan_management);
    const urlDirty = Boolean(settings) && publicBaseUrl.trim() !== (settings?.public_base_url || "").trim();
    return { rotation: rotationDirty, lan: lanDirty, url: urlDirty };
  }, [rotation, settings, strategy, savedStrategy, stickyLimit, savedSticky, allowLan, publicBaseUrl]);
  const changeCount = Number(dirty.rotation) + Number(dirty.lan) + Number(dirty.url);
  const dirtySections: SettingsSectionId[] = [
    ...(dirty.rotation ? (["rotation"] as const) : []),
    ...(dirty.lan || dirty.url ? (["access"] as const) : []),
  ];
  const canSave = changeCount > 0 && !saving && !(dirty.rotation && stickyError);

  const discard = () => {
    setStrategy(savedStrategy);
    setStickyLimit(String(savedSticky));
    setAllowLan(Boolean(settings?.allow_lan_management));
    setPublicBaseUrl(settings?.public_base_url || "");
  };

  const save = useCallback(async () => {
    if (!canSave) return;
    setSaving(true);
    const notices: string[] = [];
    try {
      if (dirty.rotation && rotation) {
        const saved = await saveAccountRotation(secret, {
          strategy,
          sticky_round_robin_limit: stickyNumber,
          provider_strategies: rotation.provider_strategies,
        });
        setRotation(saved);
        setStrategy(saved.strategy || DEFAULT_STRATEGY);
        setStickyLimit(String(saved.sticky_round_robin_limit || DEFAULT_STICKY));
        notices.push(t("settings.rotationSaved"));
      }
      // LAN access and the public URL keep their separate PATCH calls, as before.
      if (dirty.lan) {
        const result = await saveGatewaySettings(secret, { allow_lan_management: allowLan });
        setAllowLan(result.allow_lan_management);
        setSettings((current) =>
          current
            ? { ...current, allow_lan_management: result.allow_lan_management, lan_ips: result.lan_ips ?? current.lan_ips }
            : current,
        );
        if (result.restart_required) notices.push(t("settings.page.lanRestartNotice"));
        else notices.push(result.allow_lan_management ? t("settings.page.lanEnabledNotice") : t("settings.page.lanDisabledNotice"));
      }
      if (dirty.url) {
        const result = await saveGatewaySettings(secret, { public_base_url: publicBaseUrl.trim() });
        setPublicBaseUrl(result.public_base_url || "");
        setSettings((current) => (current ? { ...current, public_base_url: result.public_base_url || "" } : current));
        notices.push(t("settings.publicBaseUrlSaved"));
      }
      if (notices.length) onNotice?.(notices.join(" · "));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.page.saveFailed"));
    } finally {
      setSaving(false);
    }
  }, [canSave, dirty, rotation, secret, strategy, stickyNumber, allowLan, publicBaseUrl, onNotice, onError, t]);

  const resetRotation = async () => {
    setResetting(true);
    try {
      await resetAccountRotationState(secret);
      onNotice?.(t("settings.rotationReset"));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.rotationResetFailed"));
    } finally {
      setResetting(false);
    }
  };

  // Ctrl/Cmd+S saves; leaving the tab with unsaved edits asks first.
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "s" || isDialogOpen()) return;
      event.preventDefault();
      void saveRef.current();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  useEffect(() => {
    if (changeCount === 0) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [changeCount]);

  // Highlight the section currently at the top of the scroll area.
  useEffect(() => {
    const scroller = document.querySelector(".main-scroll");
    if (!scroller) return;
    let frame = 0;
    const update = () => {
      frame = 0;
      const top = scroller.getBoundingClientRect().top + 140;
      let current: SettingsSectionId = SETTINGS_SECTIONS[0].id;
      for (const section of SETTINGS_SECTIONS) {
        const element = document.getElementById(`settings-${section.id}`);
        if (element && element.getBoundingClientRect().top <= top) current = section.id;
      }
      // At the very bottom the last short panels can never reach the top.
      if (scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 4) {
        current = SETTINGS_SECTIONS[SETTINGS_SECTIONS.length - 1].id;
      }
      setActiveSection(current);
    };
    const onScroll = () => {
      if (!frame) frame = window.requestAnimationFrame(update);
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    update();
    return () => {
      scroller.removeEventListener("scroll", onScroll);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, [loading]);

  const jumpTo = (id: SettingsSectionId) => {
    setActiveSection(id);
    document.getElementById(`settings-${id}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const providerNames = useMemo(
    () => Object.fromEntries(providers.map((provider) => [provider.ID, provider.Name || provider.ID])),
    [providers],
  );

  return (
    <section className="cp-page st-page">
      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("settings.page.guideTitle")}
        steps={[
          { icon: "sync_alt", text: t("settings.page.guideStep1") },
          { icon: "save", text: t("settings.page.guideStep2") },
          { icon: "backup", text: t("settings.page.guideStep3") },
        ]}
        legend={
          <>
            <LegendItem tone="warning">{t("settings.page.unsaved")}</LegendItem>
            <LegendItem tone="muted">{t("settings.page.legendReadOnly")}</LegendItem>
          </>
        }
      />

      <div className="st-layout">
        <nav className="st-nav" aria-label={t("settings.page.sectionIndex")}>
          <span className="st-nav-kicker">{t("settings.page.sectionIndex")}</span>
          <ul>
            {SETTINGS_SECTIONS.map((section) => (
              <li key={section.id}>
                <button
                  type="button"
                  className={cn("st-nav-item", activeSection === section.id && "is-active")}
                  aria-current={activeSection === section.id ? "true" : undefined}
                  onClick={() => jumpTo(section.id)}
                >
                  <span className="material-symbols-outlined" aria-hidden>{section.icon}</span>
                  <span className="st-nav-label">{t(`settings.page.nav.${section.id}`)}</span>
                  {dirtySections.includes(section.id) ? (
                    <span className="st-nav-dot" title={t("settings.page.unsaved")} aria-label={t("settings.page.unsaved")} />
                  ) : section.readOnly ? (
                    <span className="material-symbols-outlined st-nav-lock" title={t("settings.page.readOnly")} aria-hidden>visibility</span>
                  ) : null}
                </button>
              </li>
            ))}
          </ul>
          <div className="st-nav-foot">
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </div>
        </nav>

        <div className="st-content">
          <RotationPanel
            loading={loading}
            dirty={dirty.rotation}
            strategy={strategy}
            onStrategyChange={setStrategy}
            stickyLimit={stickyLimit}
            onStickyLimitChange={setStickyLimit}
            stickyError={dirty.rotation ? stickyError : undefined}
            overrides={rotation?.provider_strategies || {}}
            providerNames={providerNames}
            resetting={resetting}
            onReset={() => setConfirmReset(true)}
          />
          <AccessPanel
            loading={loading}
            dirty={dirty.lan || dirty.url}
            settings={settings}
            allowLan={allowLan}
            onAllowLanChange={setAllowLan}
            publicBaseUrl={publicBaseUrl}
            onPublicBaseUrlChange={setPublicBaseUrl}
          />
          <PasswordPanel secret={secret} onError={onError} onNotice={onNotice} onPasswordChanged={onPasswordChanged} />
          <BackupPanel
            secret={secret}
            hasUnsavedChanges={changeCount > 0}
            onError={onError}
            onNotice={onNotice}
            onMutated={onMutated}
            onReloadSettings={load}
          />
          <FeaturesPanel loading={loading} settings={settings} />
          <RetentionPanel loading={loading} settings={settings} />

          {changeCount > 0 || saving ? (
            <div className="st-savebar" role="region" aria-label={t("settings.page.saveBar")}>
              <span className="material-symbols-outlined st-savebar-icon" aria-hidden>edit_note</span>
              <div className="st-savebar-copy">
                <strong>{t("settings.page.unsavedCount", { count: changeCount })}</strong>
                <span>
                  {dirtySections.map((id) => t(`settings.page.nav.${id}`)).join(" · ")}
                  <kbd className="cp-kbd">{modKeyLabel()} S</kbd>
                </span>
              </div>
              <div className="st-savebar-actions">
                <Button variant="ghost" size="sm" disabled={saving} onClick={() => setConfirmDiscard(true)}>
                  {t("settings.page.discard")}
                </Button>
                <Button variant="primary" size="sm" icon="save" loading={saving} disabled={!canSave} onClick={() => void save()}>
                  {saving ? t("settings.page.saving") : t("settings.page.saveChanges")}
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      </div>

      <ConfirmDialog
        open={confirmReset}
        title={t("settings.page.resetConfirmTitle")}
        message={t("settings.page.resetConfirmMessage")}
        confirmText={t("settings.resetRuntimeState")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => setConfirmReset(false)}
        onConfirm={() => {
          setConfirmReset(false);
          void resetRotation();
        }}
      />
      <ConfirmDialog
        open={confirmDiscard}
        title={t("settings.page.discardConfirmTitle")}
        message={t("settings.page.discardConfirmMessage", { count: changeCount })}
        confirmText={t("settings.page.discard")}
        cancelText={t("settings.page.keepEditing")}
        variant="danger"
        onClose={() => setConfirmDiscard(false)}
        onConfirm={() => {
          setConfirmDiscard(false);
          discard();
        }}
      />
    </section>
  );
}
