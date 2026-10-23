import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import { useTranslation } from "react-i18next";
import { useViewportWidth } from "../../hooks/useViewportWidth";
import { GuideToggle, IconAction, PageGuide, isDialogOpen, useStoredState, usePageGuide } from "../page-kit";
import { cn } from "../ui";
import { streamChatCompletion, type ChatRequestOptions } from "./api";
import { ChatComposer } from "./ChatComposer";
import { ChatModelPicker, useGroupLabel } from "./ChatModelPicker";
import { ChatSettingsPanel, useUsableApiKeys, type ChatApiKeyOption } from "./ChatSettingsPanel";
import { ChatSidebar } from "./ChatSidebar";
import { ChatEmptyState, ChatThread } from "./ChatThread";
import type { ChatAttachment, ChatMessage, ChatModelOption, ChatParams, ChatSession } from "./types";
import {
  STORAGE_KEYS,
  buildUserContent,
  cloneSession,
  createId,
  fileToDataUrl,
  makeSessionTitle,
  modKeyLabel,
  safeParse,
  textValue,
} from "./utils";

type Props = {
  models: ChatModelOption[];
  loadingProviderModels?: boolean;
  loadingProviderIds?: string[];
  providerLabels?: Record<string, string>;
  providerError?: string;
  apiKey: string;
  /** Client API-key records, used to offer keys whose secret this browser has saved. */
  apiKeys?: ChatApiKeyOption[];
  onApiKeyChange: (value: string) => void;
  onInvalidClientApiKey: () => void;
  onError: (message: string) => void;
};

const EMPTY_PARAMS: ChatParams = { systemPrompt: "", temperature: "", maxTokens: "" };
/** Below this width the history and settings become overlays. */
const NARROW = 900;

function requestOptions(params: ChatParams): ChatRequestOptions {
  const options: ChatRequestOptions = {};
  const temperature = Number(params.temperature);
  if (params.temperature.trim() && Number.isFinite(temperature)) options.temperature = temperature;
  const maxTokens = Number(params.maxTokens);
  if (params.maxTokens.trim() && Number.isInteger(maxTokens) && maxTokens > 0) options.max_tokens = maxTokens;
  return options;
}

export function ChatView({
  models,
  loadingProviderModels = false,
  loadingProviderIds = [],
  providerLabels = {},
  providerError = "",
  apiKey,
  apiKeys = [],
  onApiKeyChange,
  onInvalidClientApiKey,
}: Props) {
  const { t } = useTranslation();
  const guide = usePageGuide("chat");
  const groupLabel = useGroupLabel();
  const narrow = useViewportWidth() < NARROW;
  const usableKeys = useUsableApiKeys(apiKeys);

  const [sessions, setSessions] = useState<ChatSession[]>(() => {
    const saved = safeParse<ChatSession[]>(localStorage.getItem(STORAGE_KEYS.sessions), []);
    return Array.isArray(saved)
      ? saved.map((session) => ({
          ...session,
          // A stream cut off by a reload can never finish; show it as stopped.
          messages: Array.isArray(session.messages)
            ? session.messages.map((message) =>
                message.status === "streaming" ? { ...message, status: "done" as const, stopped: true } : message,
              )
            : [],
        }))
      : [];
  });
  const [activeSessionId, setActiveSessionId] = useState(() => localStorage.getItem(STORAGE_KEYS.activeSessionId) || "");
  const [activeModelId, setActiveModelId] = useState(() => localStorage.getItem(STORAGE_KEYS.activeModelId) || "");
  const [draft, setDraft] = useState(() => localStorage.getItem(STORAGE_KEYS.draft) || "");
  const [params, setParams] = useState<ChatParams>(() => ({
    ...EMPTY_PARAMS,
    ...safeParse<Partial<ChatParams>>(localStorage.getItem(STORAGE_KEYS.params), {}),
  }));
  const [attachments, setAttachments] = useState<ChatAttachment[]>([]);
  const [isSending, setIsSending] = useState(false);
  const [streamingMessageId, setStreamingMessageId] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [sidebarPref, setSidebarPref] = useStoredState<"open" | "closed">(STORAGE_KEYS.sidebar, "open", ["open", "closed"]);
  const [panelPref, setPanelPref] = useStoredState<"open" | "closed">(STORAGE_KEYS.panel, "closed", ["open", "closed"]);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [mobilePanel, setMobilePanel] = useState(false);
  const [chatError, setChatError] = useState("");

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const stickToBottomRef = useRef(true);
  const initializedRef = useRef(false);

  const sidebarShown = narrow ? drawerOpen : sidebarPref === "open";
  const panelShown = narrow ? mobilePanel : panelPref === "open";
  const setPanel = (open: boolean) => (narrow ? setMobilePanel(open) : setPanelPref(open ? "open" : "closed"));

  const modelIndex = useMemo(() => new Map(models.map((model) => [model.id, model])), [models]);

  const activeModel = useMemo(() => {
    if (activeModelId && modelIndex.has(activeModelId)) return modelIndex.get(activeModelId)!;
    if (activeSessionId) {
      const session = sessions.find((item) => item.id === activeSessionId);
      if (session?.modelId && modelIndex.has(session.modelId)) return modelIndex.get(session.modelId)!;
    }
    return models[0] || null;
  }, [activeModelId, modelIndex, activeSessionId, sessions, models]);

  const currentSession = useMemo(
    () => sessions.find((session) => session.id === activeSessionId) || null,
    [sessions, activeSessionId],
  );
  const currentMessages = currentSession?.messages || [];
  const hasKey = Boolean(apiKey.trim());
  const canSend = !isSending && !!activeModel && hasKey && (draft.trim().length > 0 || attachments.length > 0);
  const keyLabel = usableKeys.find((key) => key.secret === apiKey.trim());

  const loadingProviders = useMemo(
    () => loadingProviderIds.map((id) => ({ id, label: providerLabels[id] || id })),
    [loadingProviderIds, providerLabels],
  );

  useEffect(() => {
    localStorage.setItem(STORAGE_KEYS.sessions, JSON.stringify(sessions));
    localStorage.setItem(STORAGE_KEYS.activeSessionId, activeSessionId);
    localStorage.setItem(STORAGE_KEYS.activeModelId, activeModelId);
    localStorage.setItem(STORAGE_KEYS.draft, draft);
  }, [sessions, activeSessionId, activeModelId, draft]);

  useEffect(() => {
    localStorage.setItem(STORAGE_KEYS.params, JSON.stringify(params));
  }, [params]);

  // Follow the stream only while the reader is at the bottom.
  useEffect(() => {
    const scroller = scrollRef.current;
    if (!scroller) return;
    // The empty state reads top-down; only a thread follows the bottom.
    if (currentMessages.length === 0) scroller.scrollTop = 0;
    else if (stickToBottomRef.current) scroller.scrollTop = scroller.scrollHeight;
  }, [currentMessages]);
  useEffect(() => {
    stickToBottomRef.current = true;
    endRef.current?.scrollIntoView({ block: "end" });
  }, [activeSessionId]);

  useEffect(() => {
    if (!chatError) return;
    const timer = window.setTimeout(() => setChatError(""), 6000);
    return () => window.clearTimeout(timer);
  }, [chatError]);

  useEffect(() => {
    if (initializedRef.current || models.length === 0) return;
    const defaultModel = activeModelId && modelIndex.has(activeModelId) ? modelIndex.get(activeModelId)! : models[0];
    if (sessions.length > 0) {
      const session = sessions.find((item) => item.id === activeSessionId) || sessions[0];
      const sessionModel = session?.modelId && modelIndex.has(session.modelId) ? modelIndex.get(session.modelId)! : defaultModel;
      initializedRef.current = true;
      setActiveSessionId(session.id);
      setActiveModelId(sessionModel.id);
      return;
    }
    const session = newSession(defaultModel);
    initializedRef.current = true;
    setSessions([session]);
    setActiveSessionId(session.id);
    setActiveModelId(defaultModel.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [models, modelIndex, sessions, activeSessionId, activeModelId]);

  function newSession(model: ChatModelOption): ChatSession {
    const now = new Date().toISOString();
    return { id: createId(), title: t("chat.newChat"), modelId: model.id, modelName: model.name, createdAt: now, updatedAt: now, messages: [] };
  }

  const updateSession = (sessionId: string, updater: (session: ChatSession) => ChatSession) => {
    setSessions((prev) => prev.map((session) => (session.id === sessionId ? updater(cloneSession(session)) : session)));
  };
  const updateMessage = (sessionId: string, messageId: string, patch: Partial<ChatMessage>) => {
    updateSession(sessionId, (session) => ({
      ...session,
      messages: session.messages.map((message) => (message.id === messageId ? { ...message, ...patch } : message)),
      updatedAt: new Date().toISOString(),
    }));
  };

  const focusComposer = () => window.requestAnimationFrame(() => textareaRef.current?.focus());

  const handleNewChat = () => {
    if (!activeModel) return;
    const session = newSession(activeModel);
    setSessions((prev) => [session, ...prev]);
    setActiveSessionId(session.id);
    setActiveModelId(session.modelId);
    setDraft("");
    setAttachments([]);
    setDrawerOpen(false);
    focusComposer();
  };

  const handleSelectSession = (sessionId: string) => {
    const session = sessions.find((item) => item.id === sessionId);
    if (!session) return;
    setActiveSessionId(sessionId);
    setActiveModelId(session.modelId);
    setDrawerOpen(false);
  };

  const handleDeleteSession = (sessionId: string) => {
    const nextSessions = sessions.filter((session) => session.id !== sessionId);
    setSessions(nextSessions);
    if (activeSessionId === sessionId) {
      const fallback = nextSessions[0] || null;
      if (fallback) {
        setActiveSessionId(fallback.id);
        setActiveModelId(fallback.modelId);
      } else {
        setActiveSessionId("");
        setActiveModelId(activeModel?.id || "");
      }
    }
  };

  const handleDeleteAll = () => {
    abortRef.current?.abort();
    if (activeModel) {
      const session = newSession(activeModel);
      setSessions([session]);
      setActiveSessionId(session.id);
    } else {
      setSessions([]);
      setActiveSessionId("");
    }
  };

  const handleSelectModel = (modelId: string) => {
    const model = modelIndex.get(modelId);
    if (!model) return;
    const current = sessions.find((session) => session.id === activeSessionId);
    if (current && current.messages.length > 0) {
      // Keep each conversation on one model: switching starts a new chat.
      const session = newSession(model);
      setSessions((prev) => [session, ...prev]);
      setActiveSessionId(session.id);
    } else if (current) {
      setSessions((prev) => prev.map((item) => (item.id === current.id ? { ...item, modelId: model.id, modelName: model.name } : item)));
    } else {
      const session = newSession(model);
      setSessions((prev) => [session, ...prev]);
      setActiveSessionId(session.id);
    }
    setActiveModelId(model.id);
    setPickerOpen(false);
    focusComposer();
  };

  const handleAttachFiles = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    const images = files.filter((file) => file.type.startsWith("image/"));
    event.target.value = "";
    if (images.length === 0) return;
    const converted = await Promise.all(
      images.map(async (file) => ({ id: createId(), name: file.name, type: file.type, dataUrl: await fileToDataUrl(file) })),
    );
    setAttachments((prev) => [...prev, ...converted]);
  };

  const handleStop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  /** Streams an assistant answer for `history` (which ends with the user turn) into the session. */
  const runCompletion = async (sessionId: string, model: ChatModelOption, history: ChatMessage[], title?: string) => {
    const assistantId = createId();
    const assistant: ChatMessage = {
      id: assistantId,
      role: "assistant",
      content: "",
      createdAt: new Date().toISOString(),
      status: "streaming",
      modelName: model.name,
    };
    setSessions((prev) =>
      prev.map((item) =>
        item.id === sessionId
          ? {
              ...item,
              modelId: model.id,
              modelName: model.name,
              messages: [...history, assistant],
              updatedAt: new Date().toISOString(),
              title: title ?? item.title,
            }
          : item,
      ),
    );
    stickToBottomRef.current = true;
    setIsSending(true);
    setStreamingMessageId(assistantId);
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    const system = params.systemPrompt.trim();
    const requestMessages = [
      ...(system ? [{ role: "system", content: system }] : []),
      ...history
        // Failed or empty assistant turns are UI notes, not model output.
        .filter((message) => message.role === "user" || (message.status !== "error" && textValue(message.content)))
        .map((message) => ({
          role: message.role,
          content: message.role === "user" ? buildUserContent(message) : message.content,
        })),
    ];

    const started = performance.now();
    let firstTokenMs: number | undefined;
    try {
      const assistantText = await streamChatCompletion(
        apiKey.trim(),
        model.requestModel || model.id,
        requestMessages,
        {
          signal: controller.signal,
          onDelta: (text) => {
            if (firstTokenMs === undefined) firstTokenMs = performance.now() - started;
            updateMessage(sessionId, assistantId, { content: text, status: "streaming" });
          },
        },
        requestOptions(params),
      );
      updateSession(sessionId, (session) => ({
        ...session,
        messages: session.messages.map((message) =>
          message.id === assistantId
            ? { ...message, content: assistantText || message.content, status: "done", firstTokenMs, durationMs: performance.now() - started }
            : message,
        ),
        updatedAt: new Date().toISOString(),
      }));
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === "AbortError") {
        // Keep whatever streamed so far and mark it as stopped.
        updateMessage(sessionId, assistantId, { status: "done", stopped: true, firstTokenMs, durationMs: performance.now() - started });
        return;
      }
      const errorText = cause instanceof Error ? cause.message : t("chat.failedToSend");
      const invalidClientApiKey = errorText.trim().toLowerCase() === "invalid client api key";
      if (invalidClientApiKey) onInvalidClientApiKey();
      // The failed turn shows the error inline (with Retry); no banner on top of it.
      const shown = invalidClientApiKey ? t("chat.invalidApiKey") : errorText;
      updateSession(sessionId, (session) => ({
        ...session,
        messages: session.messages.map((message) =>
          message.id === assistantId
            ? { ...message, content: message.content || `Error: ${shown}`, status: "error", durationMs: performance.now() - started }
            : message,
        ),
        updatedAt: new Date().toISOString(),
      }));
    } finally {
      if (abortRef.current === controller) {
        setIsSending(false);
        setStreamingMessageId("");
        abortRef.current = null;
      }
    }
  };

  const sendMessage = async () => {
    const model = activeModel;
    if (!hasKey) {
      setChatError(t("chat.apiKeyRequired"));
      setPanel(true);
      return;
    }
    if (!model) return;
    const userText = draft.trim();
    if (!userText && attachments.length === 0) return;

    let sessionId = activeSessionId;
    let session = sessions.find((item) => item.id === sessionId);
    if (!session) {
      const created = newSession(model);
      sessionId = created.id;
      session = created;
      setSessions((prev) => [created, ...prev]);
      setActiveSessionId(sessionId);
    }

    const userMessage: ChatMessage = {
      id: createId(),
      role: "user",
      content: userText,
      attachments: attachments.map((attachment) => ({ ...attachment })),
      createdAt: new Date().toISOString(),
    };
    const title = session.messages.length === 0 ? makeSessionTitle(userText, t("chat.imageMessage")) : undefined;
    setDraft("");
    setAttachments([]);
    await runCompletion(sessionId, model, [...session.messages, userMessage], title);
  };

  const regenerate = () => {
    if (!currentSession || !activeModel || isSending) return;
    if (!hasKey) {
      setChatError(t("chat.apiKeyRequired"));
      setPanel(true);
      return;
    }
    const messages = currentSession.messages;
    let index = messages.length - 1;
    while (index >= 0 && messages[index].role !== "assistant") index -= 1;
    if (index < 0) return;
    void runCompletion(currentSession.id, activeModel, messages.slice(0, index));
  };

  const applyPreset = (prompt: string) => {
    setDraft((current) => (current.trim() ? `${current}\n\n${prompt}` : prompt));
    focusComposer();
  };

  // Esc stops a running answer; Ctrl/Cmd+K opens the model picker.
  const sendingRef = useRef(isSending);
  sendingRef.current = isSending;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && sendingRef.current && !isDialogOpen()) {
        event.preventDefault();
        handleStop();
        return;
      }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k" && !isDialogOpen()) {
        event.preventDefault();
        setPickerOpen(true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [handleStop]);

  const paramsSummary = [
    params.systemPrompt.trim() ? t("chat.systemShort") : "",
    requestOptions(params).temperature !== undefined ? `T ${requestOptions(params).temperature}` : "",
    requestOptions(params).max_tokens !== undefined ? t("chat.maxShort", { count: requestOptions(params).max_tokens }) : "",
  ].filter(Boolean).join(" · ");

  const blockedReason = !activeModel ? t("chat.blockedNoModel") : !hasKey ? t("chat.blockedNoKey") : undefined;

  return (
    <section className="ch-page">
      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("chat.guideTitle")}
        steps={[
          { icon: "smart_toy", text: t("chat.guideStep1") },
          { icon: "vpn_key", text: t("chat.guideStep2") },
          { icon: "keyboard", text: t("chat.guideStep3") },
        ]}
      />

      <div className={cn("ch-shell", !sidebarShown && "is-sidebar-hidden", panelShown && "is-panel-open")}>
        {sidebarShown ? (
          <>
            {narrow ? <button type="button" className="ch-backdrop" aria-label={t("chat.close")} onClick={() => setDrawerOpen(false)} /> : null}
            <ChatSidebar
              sessions={sessions}
              activeSessionId={activeSessionId}
              canCreate={Boolean(activeModel)}
              onNewChat={handleNewChat}
              onSelect={handleSelectSession}
              onDelete={handleDeleteSession}
              onDeleteAll={handleDeleteAll}
              onCollapse={() => (narrow ? setDrawerOpen(false) : setSidebarPref("closed"))}
            />
          </>
        ) : null}

        <div className="ch-main">
          <header className="ch-bar">
            {!sidebarShown ? (
              <IconAction
                icon={narrow ? "history" : "left_panel_open"}
                label={t("chat.expandHistory")}
                bordered
                onClick={() => (narrow ? setDrawerOpen(true) : setSidebarPref("open"))}
              />
            ) : null}
            <button
              type="button"
              className="ch-model-trigger"
              onClick={() => setPickerOpen(true)}
              disabled={models.length === 0 && loadingProviders.length === 0}
              title={`${t("chat.changeModel")} (${modKeyLabel()} K)`}
            >
              <span className="material-symbols-outlined ch-model-trigger-icon" aria-hidden>smart_toy</span>
              <span className="ch-model-trigger-text">
                <strong>{activeModel?.name || t("chat.selectModel")}</strong>
                <span>
                  {activeModel ? `${groupLabel(activeModel.group)} · ${activeModel.requestModel || activeModel.id}` : t("chat.noModelsConfigured")}
                </span>
              </span>
              {loadingProviderModels ? (
                <span className="material-symbols-outlined animate-spin ch-model-trigger-spin" aria-hidden>progress_activity</span>
              ) : null}
              <kbd className="cp-kbd">{modKeyLabel()} K</kbd>
              <span className="material-symbols-outlined" aria-hidden>expand_more</span>
            </button>

            <div className="ch-bar-end">
              <button
                type="button"
                className={cn("ch-key-pill cp-status", hasKey ? "cp-tone-success" : "cp-tone-warning")}
                onClick={() => setPanel(true)}
                title={t("chat.clientApiKey")}
              >
                <span className="material-symbols-outlined" aria-hidden>{hasKey ? "vpn_key" : "key_off"}</span>
                <span>{hasKey ? keyLabel?.name || keyLabel?.id || t("chat.customKeyShort") : t("chat.noKey")}</span>
              </button>
              <IconAction
                icon="tune"
                label={t("chat.settingsTitle")}
                bordered
                active={panelShown}
                aria-pressed={panelShown}
                onClick={() => setPanel(!panelShown)}
              />
              <GuideToggle open={guide.open} onToggle={guide.toggle} />
            </div>
          </header>

          {chatError ? (
            <div className="ch-error" role="alert">
              <span className="material-symbols-outlined" aria-hidden>error</span>
              <span>{chatError}</span>
              <IconAction icon="close" label={t("common.dismissError")} onClick={() => setChatError("")} />
            </div>
          ) : null}

          <div
            ref={scrollRef}
            className="ch-scroll custom-scrollbar"
            onScroll={(event) => {
              const element = event.currentTarget;
              stickToBottomRef.current = element.scrollHeight - element.scrollTop - element.clientHeight < 80;
            }}
          >
            {currentMessages.length === 0 ? (
              <ChatEmptyState
                noModels={models.length === 0 && !loadingProviderModels}
                modelName={activeModel?.name}
                hasKey={hasKey}
                onOpenPicker={() => setPickerOpen(true)}
                onOpenSettings={() => setPanel(true)}
                onPreset={applyPreset}
              />
            ) : (
              <ChatThread
                ref={endRef}
                messages={currentMessages}
                streamingMessageId={streamingMessageId}
                fallbackModelName={currentSession?.modelName || activeModel?.name || ""}
                canRegenerate={!isSending && Boolean(activeModel)}
                onRegenerate={regenerate}
              />
            )}
          </div>

          <ChatComposer
            ref={textareaRef}
            draft={draft}
            onDraftChange={setDraft}
            attachments={attachments}
            onAttach={(event) => void handleAttachFiles(event)}
            onRemoveAttachment={(id) => setAttachments((prev) => prev.filter((attachment) => attachment.id !== id))}
            canSend={canSend}
            isSending={isSending}
            disabled={!activeModel}
            blockedReason={blockedReason}
            modelName={activeModel?.name}
            paramsSummary={paramsSummary}
            onSend={() => void sendMessage()}
            onStop={handleStop}
            onOpenPicker={() => setPickerOpen(true)}
            onOpenSettings={() => setPanel(true)}
          />
        </div>

        {panelShown ? (
          <>
            {narrow ? <button type="button" className="ch-backdrop" aria-label={t("chat.close")} onClick={() => setPanel(false)} /> : null}
            <ChatSettingsPanel
              apiKeys={apiKeys}
              apiKey={apiKey}
              onApiKeyChange={onApiKeyChange}
              params={params}
              onParamsChange={setParams}
              onClose={() => setPanel(false)}
            />
          </>
        ) : null}
      </div>

      <ChatModelPicker
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        models={models}
        activeModelId={activeModel?.id || ""}
        onSelect={handleSelectModel}
        loadingProviders={loadingProviders}
        providerError={providerError}
      />
    </section>
  );
}
