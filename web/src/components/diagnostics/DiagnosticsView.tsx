import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Button } from "../ui";
import {
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  IconAction,
  LinkButton,
  PageGuide,
  PageNote,
  SectionHead,
  Segmented,
  StatusPill,
  Tag,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  isDialogOpen,
  usePageGuide,
} from "../page-kit";
import { previewTranslation, type TranslationPreview } from "./api";
import { JsonView } from "./JsonView";
import {
  SOURCE_PROTOCOLS,
  TARGET_PROTOCOLS,
  byteSize,
  checkJson,
  exampleText,
  isPristineBody,
  type SourceProtocol,
  type TargetProtocol,
} from "./protocols";

type ResultView = "outbound" | "canonical";
type Run = { key: string; data?: TranslationPreview; error?: string };

const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

export function DiagnosticsView({ secret }: { secret: string }) {
  const { t } = useTranslation();
  const guide = usePageGuide("diagnostics");
  const { copied, copy } = useCopyToClipboard();
  const [source, setSource] = useState<SourceProtocol>("openai");
  const [target, setTarget] = useState<TargetProtocol>("codex");
  const [model, setModel] = useState("diagnostic-model");
  const [body, setBody] = useState(() => exampleText("openai"));
  const [view, setView] = useState<ResultView>("outbound");
  const [run, setRun] = useState<Run | null>(null);
  const [busy, setBusy] = useState(false);
  const editorRef = useRef<HTMLTextAreaElement>(null);

  const json = useMemo(() => checkJson(body), [body]);
  const sourceInfo = SOURCE_PROTOCOLS.find((item) => item.value === source)!;
  const targetInfo = TARGET_PROTOCOLS.find((item) => item.value === target)!;
  const inputKey = `${source}\u0000${target}\u0000${model}\u0000${body}`;
  const stale = Boolean(run && run.key !== inputKey);
  const canRun = json.ok && model.trim() !== "" && !busy;

  const preview = useCallback(async () => {
    if (!json.ok || !model.trim()) return;
    setBusy(true);
    const key = inputKey;
    try {
      const data = await previewTranslation(secret, { source, target, model, body: json.value });
      setRun({ key, data });
    } catch (cause) {
      setRun({ key, error: cause instanceof Error ? cause.message : String(cause) });
    } finally {
      setBusy(false);
    }
  }, [inputKey, json, model, secret, source, target]);

  const previewRef = useRef(preview);
  previewRef.current = preview;

  // Ctrl/Cmd+Enter runs the preview from anywhere on the page.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Enter" || !(event.metaKey || event.ctrlKey) || isDialogOpen()) return;
      event.preventDefault();
      void previewRef.current();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Switching the upstream format re-runs a preview that is otherwise current,
  // so comparing formats is one click per format.
  const rerunOnTarget = useRef(false);
  useEffect(() => {
    if (!rerunOnTarget.current) return;
    rerunOnTarget.current = false;
    void previewRef.current();
  }, [target]);

  const changeSource = (next: SourceProtocol) => {
    setSource(next);
    // Only replace an untouched example: a pasted request is kept so the
    // operator can paste first and pick its protocol afterwards.
    if (isPristineBody(body)) setBody(exampleText(next));
    setRun(null);
  };

  const changeTarget = (next: TargetProtocol) => {
    if (next === target) return;
    rerunOnTarget.current = Boolean(run?.data && !stale && json.ok);
    setTarget(next);
  };

  const formatBody = () => {
    if (json.ok) setBody(JSON.stringify(json.value, null, 2));
  };

  const loadExample = () => {
    setBody(exampleText(source));
    editorRef.current?.focus();
  };

  const result = run?.data;
  const resultText = useMemo(
    () => (result ? JSON.stringify(view === "outbound" ? result.outbound : result.canonical, null, 2) : ""),
    [result, view],
  );
  const lineCount = body ? body.split("\n").length : 0;
  const canonical = result?.canonical;
  const shortcut = IS_MAC ? "⌘ Enter" : "Ctrl Enter";
  const yesNo = (value: unknown) => (value ? t("diagnostics.yes") : t("diagnostics.no"));

  return (
    <section className="cp-page dg-page">
      <Toolbar>
        <ToolbarRow>
          <div className="dg-flow">
            <span className="dg-flow-label">{t("diagnostics.from")}</span>
            <FilterTabs
              label={t("diagnostics.source")}
              value={source}
              onChange={changeSource}
              items={SOURCE_PROTOCOLS.map((item) => ({
                value: item.value,
                label: item.label,
                title: t("diagnostics.sourceTitle", { endpoint: item.endpoint }),
              }))}
            />
          </div>
          <ToolbarEnd>
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
            <Button
              variant="primary"
              size="sm"
              icon="transform"
              loading={busy}
              disabled={!canRun}
              onClick={() => void preview()}
              title={`${t("diagnostics.preview")} (${shortcut})`}
            >
              {t("diagnostics.preview")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <div className="dg-flow">
            <span className="dg-flow-label">{t("diagnostics.to")}</span>
            <FilterTabs
              label={t("diagnostics.target")}
              value={target}
              onChange={changeTarget}
              items={TARGET_PROTOCOLS.map((item) => ({
                value: item.value,
                label: item.label,
                title: t(`diagnostics.targetTitle.${item.value}`),
              }))}
            />
          </div>
          <ToolbarEnd>
            <label className="dg-model" title={t("diagnostics.modelHint")}>
              <span className="dg-model-label">{t("diagnostics.model")}</span>
              <input
                value={model}
                onChange={(event) => setModel(event.target.value)}
                placeholder={t("diagnostics.modelPlaceholder")}
                aria-label={t("diagnostics.model")}
                spellCheck={false}
                required
              />
            </label>
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("diagnostics.guide.title")}
        steps={[
          { icon: "input", text: t("diagnostics.guide.input") },
          { icon: "transform", text: t("diagnostics.guide.output") },
          { icon: "shield", text: t("diagnostics.guide.offline") },
        ]}
      />

      <div className="dg-workspace">
        <section className="cp-panel dg-pane" aria-label={t("diagnostics.inputTitle")}>
          <SectionHead
            leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>input</span>}
            title={t("diagnostics.inputTitle")}
            subtitle={`${sourceInfo.label} · ${sourceInfo.endpoint}`}
            meta={
              json.ok ? (
                <StatusPill tone="success">{t("diagnostics.jsonValid")}</StatusPill>
              ) : (
                <StatusPill tone="danger">{t("diagnostics.jsonInvalid")}</StatusPill>
              )
            }
            actions={
              <>
                <IconAction icon="data_object" label={t("diagnostics.format")} disabled={!json.ok} onClick={formatBody} />
                <IconAction icon="restart_alt" label={t("diagnostics.loadExample", { label: sourceInfo.label })} onClick={loadExample} />
              </>
            }
          />
          <textarea
            ref={editorRef}
            className="dg-editor custom-scrollbar"
            aria-label={t("diagnostics.input")}
            aria-invalid={!json.ok}
            value={body}
            onChange={(event) => setBody(event.target.value)}
            spellCheck={false}
          />
          {!json.ok ? (
            <p className="dg-inline-error" role="alert">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              <span>{json.message || t("diagnostics.notObject")}</span>
            </p>
          ) : null}
          <footer className="dg-pane-foot">
            <span>{t("diagnostics.inputStats", { count: lineCount, size: byteSize(body) })}</span>
            <span className="dg-shortcut"><kbd className="dg-kbd">{shortcut}</kbd> {t("diagnostics.shortcut")}</span>
          </footer>
        </section>

        <section className="cp-panel dg-pane" aria-label={t("diagnostics.outputTitle")} aria-busy={busy}>
          <SectionHead
            leading={<span className="material-symbols-outlined cp-section-icon" aria-hidden>output</span>}
            title={t("diagnostics.outputTitle")}
            subtitle={`${sourceInfo.label} → ${targetInfo.label}`}
            actions={
              result ? (
                <>
                  <Segmented
                    label={t("diagnostics.viewLabel")}
                    value={view}
                    onChange={setView}
                    items={[
                      { value: "outbound", label: t("diagnostics.outbound") },
                      { value: "canonical", label: t("diagnostics.canonical") },
                    ]}
                  />
                  <IconAction
                    icon={copied === "result" ? "check" : "content_copy"}
                    label={copied === "result" ? t("common.copied") : t("diagnostics.copyView", { view: t(`diagnostics.${view}`) })}
                    onClick={() => copy(resultText, "result")}
                  />
                </>
              ) : null
            }
          />

          {stale && !busy ? (
            <PageNote icon="update" action={<LinkButton onClick={() => void preview()} disabled={!canRun}>{t("diagnostics.rerun")}</LinkButton>}>
              {t("diagnostics.staleNote")}
            </PageNote>
          ) : null}

          {busy && !result ? (
            <div className="cp-skeleton dg-skeleton" aria-label={t("diagnostics.running")}>
              <span />
              <span />
              <span />
            </div>
          ) : run?.error ? (
            <div className="dg-error" role="alert">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              <div>
                <strong>{t("diagnostics.failed")}</strong>
                <p>{run.error}</p>
              </div>
            </div>
          ) : result && canonical ? (
            <div className={stale ? "dg-result is-stale" : "dg-result"}>
              <dl className="cp-facts dg-facts">
                <div><dt>{t("diagnostics.facts.model")}</dt><dd title={String(canonical.upstream_model || "")}>{String(canonical.upstream_model || "—")}</dd></div>
                <div><dt>{t("diagnostics.facts.messages")}</dt><dd>{Array.isArray(canonical.messages) ? canonical.messages.length : 0}</dd></div>
                <div><dt>{t("diagnostics.facts.system")}</dt><dd>{yesNo(canonical.system)}</dd></div>
                <div><dt>{t("diagnostics.facts.tools")}</dt><dd>{Array.isArray(canonical.tools) ? canonical.tools.length : 0}</dd></div>
                <div><dt>{t("diagnostics.facts.stream")}</dt><dd>{yesNo(canonical.stream)}</dd></div>
                <div><dt>{t("diagnostics.facts.maxTokens")}</dt><dd>{canonical.max_tokens || "—"}</dd></div>
                <div><dt>{t("diagnostics.facts.size")}</dt><dd>{byteSize(resultText)}</dd></div>
              </dl>
              <JsonView text={resultText} label={t(`diagnostics.${view}`)} />
              <div className="dg-notes">
                {result.offline ? <Tag tone="success">{t("diagnostics.offline")}</Tag> : null}
                {(result.notes || []).map((note) => (
                  <span key={note} className="dg-note">{note}</span>
                ))}
              </div>
            </div>
          ) : (
            <EmptyPanel
              compact
              icon="transform"
              title={t("diagnostics.emptyTitle")}
              description={t("diagnostics.emptyDesc", { shortcut })}
            />
          )}
        </section>
      </div>
    </section>
  );
}
