import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  EmptyPanel,
  GuideToggle,
  IconAction,
  LegendItem,
  PageGuide,
  PageNote,
  PageStats,
  SectionHead,
  StatusPill,
  Tag,
  usePageGuide,
} from "../page-kit";
import { cn } from "../ui";
import { fetchUsageEvents, type UsageEvent } from "../usage/api";
import { fmt, fmtCompact } from "../usage/utils";
import { fetchTokenSaverSettings, updateTokenSaverSettings, type CompressionMode, type TokenSaverSettings } from "./api";
import { SavingsChart } from "./SavingsChart";

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
};

// Engines each mode runs, in order. Names are product names and stay untranslated.
const COMPRESSION_MODES: Array<{ id: CompressionMode; engines: string[]; recommended?: boolean }> = [
  { id: "stacked", engines: ["RTK", "Caveman"], recommended: true },
  { id: "full", engines: ["RTK", "CCR", "Headroom", "Caveman"] },
  { id: "ultra", engines: ["RTK", "CCR", "Headroom", "LLMLingua", "Caveman"] },
  { id: "rtk", engines: ["RTK"] },
  { id: "caveman", engines: ["Caveman"] },
  { id: "lite", engines: ["Headroom"] },
  { id: "off", engines: [] },
];

const INSTALL_COMMANDS = ["cargo install --git https://github.com/rtk-ai/rtk", "rtk init -g"];
const OVERRIDE_HEADERS = ["X-TProxy-Compression: stacked", "X-TProxy-Token-Saver: off"];
/** Savings are measured over the newest requests in the usage log. */
const SAMPLE_SIZE = 200;

export function TokenSaverView({ secret, onError, onNotice }: Props) {
  const { t } = useTranslation();
  const guide = usePageGuide("token-saver");
  const [settings, setSettings] = useState<TokenSaverSettings | null>(null);
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [compressionMode, setCompressionMode] = useState<CompressionMode>("stacked");
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [saving, setSaving] = useState<CompressionMode | null>(null);
  const [events, setEvents] = useState<UsageEvent[] | null>(null);
  const [copied, setCopied] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchTokenSaverSettings(secret);
      setSettings(data);
      setRtkEnabled(data.rtk_enabled !== false);
      const mode = (data.compression_mode || "stacked") as CompressionMode;
      setCompressionMode(COMPRESSION_MODES.some((item) => item.id === mode) ? mode : "stacked");
      setLoadError("");
    } catch (error) {
      const message = error instanceof Error ? error.message : t("tokenSaver.failedLoad");
      setLoadError(message);
      onError(message);
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  // Savings are a read-only extra: if the usage log is unavailable the page still works.
  useEffect(() => {
    let cancelled = false;
    fetchUsageEvents(secret, { limit: SAMPLE_SIZE })
      .then((result) => {
        if (!cancelled) setEvents(result.data || []);
      })
      .catch(() => {
        if (!cancelled) setEvents([]);
      });
    return () => {
      cancelled = true;
    };
  }, [secret]);

  const saveMode = async (nextMode: CompressionMode) => {
    if (saving || nextMode === compressionMode) return;
    setSaving(nextMode);
    try {
      const nextEnabled = nextMode !== "off";
      await updateTokenSaverSettings(secret, { compression_mode: nextMode, rtk_enabled: nextEnabled });
      setCompressionMode(nextMode);
      setRtkEnabled(nextEnabled);
      onNotice?.(t("tokenSaver.modeSaved", { mode: t(`tokenSaver.modes.${nextMode}.name`) }));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("tokenSaver.failedUpdate"));
    } finally {
      setSaving(null);
    }
  };

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      window.setTimeout(() => setCopied((current) => (current === text ? "" : current)), 1500);
      onNotice?.(t("common.copied"));
    } catch {
      onError(t("tokenSaver.copyFailed"));
    }
  };

  const savings = useMemo(() => {
    const list = events ?? [];
    const ok = list.filter((event) => event.status >= 200 && event.status < 400);
    const saved = ok.reduce((sum, event) => sum + (event.tokens_saved || 0), 0);
    const input = ok.reduce((sum, event) => sum + (event.input_tokens || 0), 0);
    const compressed = ok.filter((event) => (event.tokens_saved || 0) > 0).length;
    return { count: list.length, ok: ok.length, saved, input, compressed, rate: saved + input > 0 ? saved / (saved + input) : 0 };
  }, [events]);

  const active = COMPRESSION_MODES.find((item) => item.id === compressionMode) ?? COMPRESSION_MODES[0];
  const isOff = !rtkEnabled || compressionMode === "off";
  const sampleReady = events !== null;

  if (!settings && loading) {
    return (
      <section className="cp-page ts-page">
        <EmptyPanel loading title={t("common.loading")} />
      </section>
    );
  }

  if (!settings && loadError) {
    return (
      <section className="cp-page ts-page">
        <EmptyPanel
          icon="cloud_off"
          title={t("tokenSaver.failedLoad")}
          description={<span role="alert">{loadError}</span>}
          action={<button type="button" className="cp-link-btn" onClick={() => void load()}>{t("usage.retry")}</button>}
        />
      </section>
    );
  }

  const onCardKey = (event: KeyboardEvent<HTMLDivElement>, mode: CompressionMode) => {
    if (event.target !== event.currentTarget) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      void saveMode(mode);
    }
  };

  return (
    <section className="cp-page ts-page">
      <PageStats
        label={t("tokenSaver.stats.label")}
        items={[
          {
            key: "mode",
            icon: "compress",
            tone: isOff ? "warning" : "success",
            label: t("tokenSaver.stats.mode"),
            value: t(`tokenSaver.modes.${compressionMode}.name`),
            hint: isOff ? t("tokenSaver.stats.modeOffHint") : active.engines.join(" → "),
          },
          {
            key: "saved",
            icon: "savings",
            label: t("tokenSaver.stats.saved"),
            value: sampleReady ? <span title={fmt(savings.saved)}>{fmtCompact(savings.saved)}</span> : "—",
            hint: t("tokenSaver.stats.sampleHint", { count: savings.count }),
            muted: savings.saved === 0,
          },
          {
            key: "rate",
            icon: "percent",
            label: t("tokenSaver.stats.rate"),
            value: savings.saved > 0 ? `${Math.round(savings.rate * 100)}%` : "—",
            hint: t("tokenSaver.stats.rateHint"),
            title: t("tokenSaver.stats.rateTitle"),
            muted: savings.saved === 0,
          },
          {
            key: "compressed",
            icon: "done_all",
            label: t("tokenSaver.stats.compressed"),
            value: sampleReady ? `${savings.compressed}/${savings.ok}` : "—",
            hint: t("tokenSaver.stats.compressedHint"),
            muted: savings.compressed === 0,
          },
          {
            key: "override",
            icon: "tune",
            label: t("tokenSaver.stats.override"),
            value: settings?.per_request_opt_out === false ? t("tokenSaver.stats.overrideOff") : t("tokenSaver.stats.overrideOn"),
            hint: t("tokenSaver.stats.overrideHint"),
          },
        ]}
      />

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("tokenSaver.guide.title")}
        steps={[
          { icon: "compress", text: t("tokenSaver.guide.what") },
          { icon: "touch_app", text: t("tokenSaver.guide.pick") },
          { icon: "terminal", text: t("tokenSaver.guide.override") },
        ]}
        legend={
          <>
            <LegendItem tone="success">{t("tokenSaver.guide.legendActive")}</LegendItem>
            <LegendItem tone="warning">{t("tokenSaver.guide.legendOff")}</LegendItem>
          </>
        }
      />

      <section className="ts-section" aria-label={t("tokenSaver.compressionPipeline")}>
        <SectionHead
          leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>compress</span>}
          title={t("tokenSaver.compressionPipeline")}
          subtitle={t("tokenSaver.pipelineSubtitle")}
          meta={saving ? <StatusPill tone="info" loading>{t("tokenSaver.saving")}</StatusPill> : null}
          actions={<GuideToggle open={guide.open} onToggle={guide.toggle} />}
        />
        <div className="cp-grid ts-modes" role="radiogroup" aria-label={t("tokenSaver.mode")}>
          {COMPRESSION_MODES.map((mode) => {
            const selected = mode.id === compressionMode;
            const pending = saving === mode.id;
            return (
              <div
                key={mode.id}
                role="radio"
                aria-checked={selected}
                aria-disabled={Boolean(saving)}
                tabIndex={selected ? 0 : -1}
                className={cn("cp-card ts-mode", selected && "is-selected", selected && (mode.id === "off" ? "ts-tone-warning" : "ts-tone-success"), saving && !pending && "is-waiting")}
                onClick={() => void saveMode(mode.id)}
                onKeyDown={(event) => {
                  onCardKey(event, mode.id);
                  if (event.key === "ArrowDown" || event.key === "ArrowRight" || event.key === "ArrowUp" || event.key === "ArrowLeft") {
                    // Radio-group arrows move focus between modes without selecting (saving is a server call).
                    event.preventDefault();
                    const cards = [...(event.currentTarget.parentElement?.querySelectorAll<HTMLElement>("[role=radio]") || [])];
                    const index = cards.indexOf(event.currentTarget);
                    const step = event.key === "ArrowDown" || event.key === "ArrowRight" ? 1 : -1;
                    cards[(index + step + cards.length) % cards.length]?.focus();
                  }
                }}
              >
                <div className="cp-card-head">
                  <span className={cn("ts-radio", selected && "is-on")} aria-hidden />
                  <div className="cp-card-title">
                    <strong>{t(`tokenSaver.modes.${mode.id}.name`)}</strong>
                    {mode.recommended || selected || pending ? (
                      <span className="cp-card-tags">
                        {mode.recommended ? <Tag tone="primary">{t("tokenSaver.recommended")}</Tag> : null}
                        {pending ? (
                          <StatusPill tone="info" loading>{t("tokenSaver.saving")}</StatusPill>
                        ) : selected ? (
                          <StatusPill tone={mode.id === "off" ? "warning" : "success"}>{t("tokenSaver.active")}</StatusPill>
                        ) : null}
                      </span>
                    ) : null}
                  </div>
                </div>
                <div className="cp-card-body">
                  {mode.engines.length ? (
                    <span className="ts-engines" aria-label={mode.engines.join(" → ")}>
                      {mode.engines.map((engine, index) => (
                        <span key={engine} className="ts-engine">
                          {index > 0 ? <span className="material-symbols-outlined" aria-hidden>arrow_forward</span> : null}
                          <code>{engine}</code>
                        </span>
                      ))}
                    </span>
                  ) : (
                    <span className="ts-engines ts-dim">{t("tokenSaver.noEngines")}</span>
                  )}
                  <p className="ts-hint">{t(`tokenSaver.modes.${mode.id}.hint`)}</p>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      <SavingsChart events={events} sampleSize={SAMPLE_SIZE} />

      <div className="ts-split">
        <section className="ts-section" aria-label={t("tokenSaver.override.title")}>
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>tune</span>}
            title={t("tokenSaver.override.title")}
            subtitle={t("tokenSaver.override.subtitle")}
          />
          <div className="ts-panel">
            {OVERRIDE_HEADERS.map((header, index) => (
              <div key={header} className="ts-command">
                <div className="ts-command-copy">
                  <small>{t(index === 0 ? "tokenSaver.override.forceMode" : "tokenSaver.override.optOut")}</small>
                  <code>{header}</code>
                </div>
                <IconAction icon={copied === header ? "check" : "content_copy"} label={t("tokenSaver.copy")} onClick={() => void copy(header)} />
              </div>
            ))}
            {settings?.per_request_opt_out === false ? <PageNote icon="block">{t("tokenSaver.override.disabled")}</PageNote> : null}
          </div>
        </section>

        <section className="ts-section" aria-label={t("tokenSaver.cliHookOptional")}>
          <SectionHead
            leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>terminal</span>}
            title={t("tokenSaver.cliHookOptional")}
            subtitle={t("tokenSaver.cliHookSubtitle")}
            meta={settings?.cli_hook_recommended ? <Tag tone="primary">{t("tokenSaver.recommended")}</Tag> : null}
          />
          <div className="ts-panel">
            <p className="ts-hint ts-panel-copy">{t("tokenSaver.cliHookDesc")}</p>
            {INSTALL_COMMANDS.map((command, index) => (
              <div key={command} className="ts-command">
                <span className="ts-step" aria-hidden>{index + 1}</span>
                <div className="ts-command-copy">
                  <small>{t(index === 0 ? "tokenSaver.installStep" : "tokenSaver.hookStep")}</small>
                  <code>{command}</code>
                </div>
                <IconAction icon={copied === command ? "check" : "content_copy"} label={t("tokenSaver.copy")} onClick={() => void copy(command)} />
              </div>
            ))}
            {settings?.upstream_project ? (
              <a className="cp-link-btn ts-link" href={settings.upstream_project} target="_blank" rel="noreferrer">
                {t("tokenSaver.upstreamProject")}
                <span className="material-symbols-outlined" aria-hidden>open_in_new</span>
              </a>
            ) : null}
          </div>
        </section>
      </div>
    </section>
  );
}
