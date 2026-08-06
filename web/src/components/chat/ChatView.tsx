import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { Badge, Button, Input, cn } from "../ui";
import { ChatMarkdown } from "./ChatMarkdown";
import { streamChatCompletion } from "./api";
import type { ChatAttachment, ChatModelOption, ChatSession } from "./types";
import {
  STORAGE_KEYS,
  buildUserContent,
  cloneSession,
  createId,
  fileToDataUrl,
  formatRelativeTime,
  makeSessionTitle,
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
  onApiKeyChange: (value: string) => void;
  onInvalidClientApiKey: () => void;
  onError: (message: string) => void;
};

export function ChatView({
  models,
  loadingProviderModels = false,
  loadingProviderIds = [],
  providerLabels = {},
  providerError = "",
  apiKey,
  onApiKeyChange,
  onInvalidClientApiKey,
  onError,
}: Props) {
  const { t } = useTranslation();
  const presetPrompts = useMemo(
    () => [
      { label: t("chat.explainSimply"), prompt: t("chat.explainSimply") },
      { label: t("chat.codeReview"), prompt: t("chat.codeReview") },
      { label: t("chat.debugHelp"), prompt: t("chat.debugHelp") },
      { label: t("chat.summarize"), prompt: t("chat.summarize") },
    ],
    [t],
  );
  const { copied, copy } = useCopyToClipboard();
  const [sessions, setSessions] = useState<ChatSession[]>(() => {
    const saved = safeParse<ChatSession[]>(localStorage.getItem(STORAGE_KEYS.sessions), []);
    return Array.isArray(saved)
      ? saved.map((session) => ({
          ...session,
          messages: Array.isArray(session.messages) ? session.messages : [],
        }))
      : [];
  });
  const [activeSessionId, setActiveSessionId] = useState(
    () => localStorage.getItem(STORAGE_KEYS.activeSessionId) || "",
  );
  const [activeModelId, setActiveModelId] = useState(
    () => localStorage.getItem(STORAGE_KEYS.activeModelId) || "",
  );
  const [draft, setDraft] = useState(() => localStorage.getItem(STORAGE_KEYS.draft) || "");
  const [attachments, setAttachments] = useState<ChatAttachment[]>([]);
  const [isSending, setIsSending] = useState(false);
  const [streamingMessageId, setStreamingMessageId] = useState("");
  const [streamingText, setStreamingText] = useState("");
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [modelSearchQuery, setModelSearchQuery] = useState("");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [chatError, setChatError] = useState("");

  const fileInputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const modelMenuRef = useRef<HTMLDivElement>(null);
  const settingsRef = useRef<HTMLDivElement>(null);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const initializedRef = useRef(false);

  const modelIndex = useMemo(() => {
    const map = new Map<string, ChatModelOption>();
    for (const model of models) map.set(model.id, model);
    return map;
  }, [models]);

  const modelGroups = useMemo(() => {
    const groups = new Map<string, ChatModelOption[]>();
    for (const model of models) {
      const items = groups.get(model.group) || [];
      items.push(model);
      groups.set(model.group, items);
    }
    return Array.from(groups.entries())
      .map(([group, items]) => ({ group, items: items.sort((a, b) => a.name.localeCompare(b.name)) }))
      .sort((a, b) => {
        const order = (group: string) => {
          if (group === "models") return 0;
          if (group === "combos") return 1;
          return 2;
        };
        const diff = order(a.group) - order(b.group);
        return diff !== 0 ? diff : a.group.localeCompare(b.group);
      });
  }, [models]);

  const filteredModelGroups = useMemo(() => {
    const query = modelSearchQuery.trim().toLowerCase();
    return modelGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((model) => {
          if (!query) return true;
          const haystack = [model.name, model.id, model.requestModel, group.group]
            .filter(Boolean)
            .join(" ")
            .toLowerCase();
          return haystack.includes(query);
        }),
      }))
      .filter((group) => group.items.length > 0);
  }, [modelGroups, modelSearchQuery]);

  const loadingProviderGroups = useMemo(
    () =>
      loadingProviderIds.map((providerId) => ({
        id: providerId,
        label: providerLabels[providerId] || providerId,
      })),
    [loadingProviderIds, providerLabels],
  );

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
  const sessionItems = useMemo(
    () => [...sessions].sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()),
    [sessions],
  );
  const canSend = !isSending && !!activeModel && !!apiKey.trim() && (draft.trim().length > 0 || attachments.length > 0);

  useEffect(() => {
    localStorage.setItem(STORAGE_KEYS.sessions, JSON.stringify(sessions));
    localStorage.setItem(STORAGE_KEYS.activeSessionId, activeSessionId);
    localStorage.setItem(STORAGE_KEYS.activeModelId, activeModelId);
    localStorage.setItem(STORAGE_KEYS.draft, draft);
  }, [sessions, activeSessionId, activeModelId, draft]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (settingsRef.current && !settingsRef.current.contains(event.target as Node)) {
        setSettingsOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  useEffect(() => {
    if (!modelMenuOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setModelMenuOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [modelMenuOpen]);

  useEffect(() => {
    if (!modelMenuOpen) setModelSearchQuery("");
  }, [modelMenuOpen]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [currentMessages, streamingText]);

  useEffect(() => {
    if (!chatError) return;
    const timer = window.setTimeout(() => setChatError(""), 6000);
    return () => window.clearTimeout(timer);
  }, [chatError]);

  useEffect(() => {
    if (initializedRef.current || models.length === 0) return;

    const defaultModel = activeModelId && modelIndex.has(activeModelId)
      ? modelIndex.get(activeModelId)!
      : models[0];

    if (sessions.length > 0) {
      const session = sessions.find((item) => item.id === activeSessionId) || sessions[0];
      const sessionModel = session?.modelId && modelIndex.has(session.modelId)
        ? modelIndex.get(session.modelId)!
        : defaultModel;
      initializedRef.current = true;
      setActiveSessionId(session.id);
      setActiveModelId(sessionModel.id);
      return;
    }

    const session: ChatSession = {
      id: createId(),
      title: t("chat.newChat"),
      modelId: defaultModel.id,
      modelName: defaultModel.name,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      messages: [],
    };

    initializedRef.current = true;
    setSessions([session]);
    setActiveSessionId(session.id);
    setActiveModelId(defaultModel.id);
  }, [models, modelIndex, sessions, activeSessionId, activeModelId]);

  const updateSession = (sessionId: string, updater: (session: ChatSession) => ChatSession) => {
    setSessions((prev) => prev.map((session) => (session.id === sessionId ? updater(cloneSession(session)) : session)));
  };

  const ensureSessionForModel = (model: ChatModelOption): ChatSession => ({
    id: createId(),
    title: t("chat.newChat"),
    modelId: model.id,
    modelName: model.name,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    messages: [],
  });

  const handleNewChat = () => {
    if (!activeModel) return;
    const session = ensureSessionForModel(activeModel);
    setSessions((prev) => [session, ...prev]);
    setActiveSessionId(session.id);
    setActiveModelId(session.modelId);
    setDraft("");
    setAttachments([]);
    setStreamingMessageId("");
    setStreamingText("");
  };

  const handleSelectSession = (sessionId: string) => {
    const session = sessions.find((item) => item.id === sessionId);
    if (!session) return;
    setActiveSessionId(sessionId);
    setActiveModelId(session.modelId);
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

  const handleSelectModel = (modelId: string) => {
    const model = modelIndex.get(modelId);
    if (!model) return;

    const current = sessions.find((session) => session.id === activeSessionId);
    if (current && current.messages.length > 0) {
      const session = ensureSessionForModel(model);
      setSessions((prev) => [session, ...prev]);
      setActiveSessionId(session.id);
    } else if (current) {
      setSessions((prev) => prev.map((item) => (item.id === current.id ? {
        ...item,
        modelId: model.id,
        modelName: model.name,
      } : item)));
    } else {
      const session = ensureSessionForModel(model);
      setSessions((prev) => [session, ...prev]);
      setActiveSessionId(session.id);
    }

    setActiveModelId(model.id);
    setModelMenuOpen(false);
  };

  const handleAttachFiles = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    if (files.length === 0) return;

    const images = files.filter((file) => file.type.startsWith("image/"));
    if (images.length === 0) {
      event.target.value = "";
      return;
    }

    const converted = await Promise.all(images.map(async (file) => ({
      id: createId(),
      name: file.name,
      type: file.type,
      dataUrl: await fileToDataUrl(file),
    })));

    setAttachments((prev) => [...prev, ...converted]);
    event.target.value = "";
  };

  const removeAttachment = (attachmentId: string) => {
    setAttachments((prev) => prev.filter((attachment) => attachment.id !== attachmentId));
  };

  const handleStop = () => {
    abortRef.current?.abort();
  };

  const sendMessage = async () => {
    const model = activeModel;
    if (!model || !apiKey.trim()) {
      if (!apiKey.trim()) {
        setChatError(t("chat.apiKeyRequired"));
        return;
      }
    }

    const userText = draft.trim();
    if (!userText && attachments.length === 0) return;

    let sessionId = activeSessionId;
    let session = sessions.find((item) => item.id === sessionId);
    if (!session) {
      const created = ensureSessionForModel(model);
      sessionId = created.id;
      session = created;
      setSessions((prev) => [created, ...prev]);
      setActiveSessionId(sessionId);
    }

    const userMessage = {
      id: createId(),
      role: "user" as const,
      content: userText,
      attachments: attachments.map((attachment) => ({ ...attachment })),
      createdAt: new Date().toISOString(),
    };

    const assistantMessageId = createId();
    const assistantMessage = {
      id: assistantMessageId,
      role: "assistant" as const,
      content: "",
      createdAt: new Date().toISOString(),
      status: "streaming" as const,
    };

    const nextMessages = [...(session.messages || []), userMessage, assistantMessage];
    setSessions((prev) => prev.map((item) => (item.id === sessionId ? {
      ...item,
      modelId: model.id,
      modelName: model.name,
      messages: nextMessages,
      updatedAt: new Date().toISOString(),
      title: item.title === t("chat.newChat") ? makeSessionTitle(userText) : item.title,
    } : item)));
    setDraft("");
    setAttachments([]);
    setIsSending(true);
    setStreamingMessageId(assistantMessageId);
    setStreamingText("");
    abortRef.current?.abort();
    abortRef.current = new AbortController();

    const requestMessages = nextMessages
      .filter((message) => !(message.role === "assistant" && message.id === assistantMessageId))
      .map((message) => ({
        role: message.role,
        content: message.role === "user" ? buildUserContent(message) : message.content,
      }));

    try {
      const assistantText = await streamChatCompletion(
        apiKey.trim(),
        model.requestModel || model.id,
        requestMessages,
        {
          signal: abortRef.current.signal,
          onDelta: (text) => {
            setStreamingText(text);
            updateSession(sessionId, (currentSession) => ({
              ...currentSession,
              messages: currentSession.messages.map((message) => (
                message.id === assistantMessageId
                  ? { ...message, content: text, status: "streaming" }
                  : message
              )),
              updatedAt: new Date().toISOString(),
            }));
          },
        },
      );

      updateSession(sessionId, (currentSession) => ({
        ...currentSession,
        messages: currentSession.messages.map((message) => (
          message.id === assistantMessageId
            ? { ...message, content: assistantText || message.content, status: "done" }
            : message
        )),
        updatedAt: new Date().toISOString(),
      }));
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === "AbortError") return;
      const errorText = cause instanceof Error ? cause.message : t("chat.failedToSend");
      const invalidClientApiKey = errorText.trim().toLowerCase() === "invalid client api key";
      if (invalidClientApiKey) onInvalidClientApiKey();
      updateSession(sessionId, (currentSession) => ({
        ...currentSession,
        messages: currentSession.messages.map((message) => (
          message.id === assistantMessageId
            ? {
                ...message,
                content: message.content || `Error: ${invalidClientApiKey ? t("chat.invalidApiKey") : errorText}`,
                status: "error",
              }
            : message
        )),
        updatedAt: new Date().toISOString(),
      }));
      setChatError(invalidClientApiKey ? t("chat.invalidApiKey") : errorText);
    } finally {
      setIsSending(false);
      setStreamingMessageId("");
      setStreamingText("");
      abortRef.current = null;
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      if (canSend) void sendMessage();
    }
  };

  const applyPreset = (prompt: string) => {
    setDraft((current) => (current.trim() ? `${current}\n\n${prompt} ` : `${prompt} `));
  };

  return (
    <section className="chat-page">
      <aside className={cn("chat-sidebar", !sidebarOpen && "is-collapsed")}>
        <div className="chat-sidebar-header">
          <Button variant="primary" size="sm" icon="add" block onClick={handleNewChat} disabled={!activeModel}>
            New chat
          </Button>
          <button
            type="button"
            className="chat-sidebar-toggle"
            onClick={() => setSidebarOpen((value) => !value)}
            aria-label={sidebarOpen ? t("chat.collapseHistory") : t("chat.expandHistory")}
          >
            <span className="material-symbols-outlined">{sidebarOpen ? "left_panel_close" : "left_panel_open"}</span>
          </button>
        </div>

        {sidebarOpen ? (
          <div className="chat-session-list custom-scrollbar">
            {sessionItems.length === 0 ? (
              <div className="chat-session-empty">No conversations yet.</div>
            ) : sessionItems.map((session) => {
              const isActive = session.id === activeSessionId;
              const latestMessage = [...session.messages].reverse().find((message) => message.role === "user");
              return (
                <div key={session.id} className={cn("chat-session-item", isActive && "is-active")}>
                  <button type="button" className="chat-session-button" onClick={() => handleSelectSession(session.id)}>
                    <span className="chat-session-title">{session.title}</span>
                    <span className="chat-session-preview">
                      {textValue(latestMessage?.content) || t("chat.emptyChat")}
                    </span>
                    <span className="chat-session-time">{formatRelativeTime(session.updatedAt)}</span>
                  </button>
                  <button
                    type="button"
                    className="chat-session-delete"
                    onClick={() => handleDeleteSession(session.id)}
                    aria-label={t("chat.deleteChat")}
                  >
                    <span className="material-symbols-outlined">delete</span>
                  </button>
                </div>
              );
            })}
          </div>
        ) : null}
      </aside>

      <div className="chat-main">
        {chatError ? (
          <div className="chat-error-banner">
            <span className="material-symbols-outlined">error</span>
            <span>{chatError}</span>
            <button type="button" className="chat-error-dismiss" onClick={() => setChatError("")} aria-label={t("common.dismissError")}>
              <span className="material-symbols-outlined">close</span>
            </button>
          </div>
        ) : null}
        <div className="chat-toolbar">
          <div ref={modelMenuRef} className="chat-model-picker">
            <button
              type="button"
              className="chat-model-trigger"
              onClick={() => setModelMenuOpen((value) => !value)}
              disabled={models.length === 0}
            >
              <span className="material-symbols-outlined">smart_toy</span>
              <div className="chat-model-trigger-text">
                <span className="chat-model-name">{activeModel?.name || t("chat.selectModel")}</span>
                <span className="chat-model-id">{activeModel?.requestModel || activeModel?.id || "No models configured"}</span>
              </div>
              <span className="material-symbols-outlined">expand_more</span>
            </button>

            {modelMenuOpen ? (
              <div className="chat-model-overlay" onClick={() => setModelMenuOpen(false)}>
                <div className="chat-model-modal" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
                  <div className="chat-model-modal-head">
                    <div>
                      <h3>Select model</h3>
                      <p>{models.length} available{loadingProviderModels ? " · discovering more…" : ""}</p>
                    </div>
                    <button
                      type="button"
                      className="chat-model-modal-close"
                      onClick={() => setModelMenuOpen(false)}
                      aria-label={t("chat.close")}
                    >
                      <span className="material-symbols-outlined">close</span>
                    </button>
                  </div>

                  <div className="chat-model-modal-search">
                    <Input
                      value={modelSearchQuery}
                      onChange={(event) => setModelSearchQuery(event.target.value)}
                      placeholder={t("chat.searchModels")}
                      icon="search"
                    />
                  </div>

                  <div className="chat-model-menu custom-scrollbar">
                    {filteredModelGroups.length === 0 && !loadingProviderModels ? (
                      <div className="chat-model-menu-empty">
                        {models.length === 0 ? (
                          <>
                            No models available. Connect providers in{" "}
                            <Link to="/providers">Providers</Link> or configure models in{" "}
                            <Link to="/models">PPM</Link>.
                          </>
                        ) : (
                          "No models match your search."
                        )}
                      </div>
                    ) : (
                      filteredModelGroups.map((group) => (
                        <div key={group.group} className="chat-model-group">
                          <div className="chat-model-group-header">
                            <span>{group.group}</span>
                            <Badge size="sm">{group.items.length}</Badge>
                          </div>
                          <div className="chat-model-grid">
                            {group.items.map((model) => {
                              const isActive = model.id === activeModelId;
                              return (
                                <button
                                  key={model.id}
                                  type="button"
                                  className={cn("chat-model-option", isActive && "is-active")}
                                  onClick={() => handleSelectModel(model.id)}
                                >
                                  <span className="chat-model-option-name">{model.name}</span>
                                  <span className="chat-model-option-id">{model.requestModel || model.id}</span>
                                  {isActive ? <span className="material-symbols-outlined">check_circle</span> : null}
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      ))
                    )}

                    {loadingProviderGroups.map((provider) => (
                      <div key={`loading-${provider.id}`} className="chat-model-group chat-model-group-loading">
                        <div className="chat-model-group-header">
                          <span>{provider.label}</span>
                          <Badge size="sm">…</Badge>
                        </div>
                        <div className="chat-model-menu-loading">
                          <span className="material-symbols-outlined animate-spin">progress_activity</span>
                          Discovering supported models…
                        </div>
                      </div>
                    ))}

                    {providerError ? <div className="chat-model-menu-error">{providerError}</div> : null}
                  </div>
                </div>
              </div>
            ) : null}
          </div>

          <div className="chat-toolbar-actions">
            <div ref={settingsRef} className="chat-settings">
              <Button
                variant="secondary"
                size="sm"
                icon="settings"
                onClick={() => setSettingsOpen((value) => !value)}
              >
                Settings
              </Button>
              {settingsOpen ? (
                <div className="chat-settings-panel">
                  <label className="chat-settings-label">Client API key</label>
                  <Input
                    type="password"
                    icon="vpn_key"
                    value={apiKey}
                    onChange={(event) => onApiKeyChange(event.target.value)}
                    placeholder="tproxy API key"
                  />
                  <p className="chat-settings-hint">
                    Uses <code>/v1/chat/completions</code> with a client API key from{" "}
                    <Link to="/apis">APIs</Link> — not the dashboard management password.
                  </p>
                </div>
              ) : null}
            </div>
          </div>
        </div>

        <div className="chat-messages custom-scrollbar">
          {currentMessages.length === 0 ? (
            <div className="chat-empty">
              <div className="chat-empty-icon">
                <span className="material-symbols-outlined">chat</span>
              </div>
              <h2>Start a conversation</h2>
              <p>
                Chat with virtual models, combos, or any model discovered from your configured provider accounts.
              </p>
              <div className="chat-presets">
                {presetPrompts.map((preset) => (
                  <button
                    key={preset.label}
                    type="button"
                    className="chat-preset"
                    onClick={() => applyPreset(preset.prompt)}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="chat-thread">
              {currentMessages.map((message) => {
                const isUser = message.role === "user";
                const isAssistant = message.role === "assistant";
                const isStreaming = isAssistant && message.id === streamingMessageId && message.status === "streaming";
                const content = textValue(message.content) || (isAssistant ? streamingText : "");

                return (
                  <div key={message.id} className={cn("chat-message", isUser ? "is-user" : "is-assistant", message.status === "error" && "is-error")}>
                    <div className="chat-message-meta">
                      <span>{isUser ? t("chat.you") : activeModel?.name || t("chat.assistant")}</span>
                    </div>

                    {message.attachments?.length ? (
                      <div className="chat-attachments">
                        {message.attachments.map((attachment) => (
                          <a
                            key={attachment.id}
                            href={attachment.dataUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="chat-attachment"
                          >
                            <img src={attachment.dataUrl} alt={attachment.name} />
                          </a>
                        ))}
                      </div>
                    ) : null}

                    <div className="chat-message-content">
                      {isAssistant ? (
                        <ChatMarkdown content={content} streaming={isStreaming} />
                      ) : (
                        content
                      )}
                    </div>

                    {content && !isStreaming ? (
                      <div className="chat-message-actions">
                        <button
                          type="button"
                          className="chat-message-copy"
                          onClick={() => copy(content, message.id)}
                          aria-label={copied === message.id ? t("common.copied") : t("chat.copyMessage")}
                          title={copied === message.id ? t("common.copied") : t("chat.copyMessage")}
                        >
                          <span className="material-symbols-outlined">
                            {copied === message.id ? "check" : "content_copy"}
                          </span>
                        </button>
                      </div>
                    ) : null}
                  </div>
                );
              })}
              <div ref={messagesEndRef} />
            </div>
          )}
        </div>

        <div className="chat-composer-wrap">
          {attachments.length > 0 ? (
            <div className="chat-composer-attachments">
              {attachments.map((attachment) => (
                <div key={attachment.id} className="chat-composer-attachment">
                  <span>{attachment.name}</span>
                  <button type="button" onClick={() => removeAttachment(attachment.id)} aria-label={t("chat.removeAttachment")}>
                    <span className="material-symbols-outlined">close</span>
                  </button>
                </div>
              ))}
            </div>
          ) : null}

          <div className="chat-composer">
            <textarea
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Message AI…"
              rows={1}
              className="chat-composer-input custom-scrollbar"
            />
            <div className="chat-composer-actions">
              <button
                type="button"
                className="chat-composer-icon"
                onClick={() => fileInputRef.current?.click()}
                disabled={!activeModel}
                aria-label="Attach image"
              >
                <span className="material-symbols-outlined">attach_file</span>
              </button>
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                multiple
                className="sr-only"
                onChange={(event) => void handleAttachFiles(event)}
              />
              <span className="chat-composer-model">{activeModel?.name || "No model"}</span>
              <div className="chat-composer-send">
                {isSending ? (
                  <button type="button" className="chat-stop-btn" onClick={handleStop} aria-label="Stop">
                    <span className="material-symbols-outlined">stop</span>
                  </button>
                ) : null}
                <button
                  type="button"
                  className={cn("chat-send-btn", canSend && "is-ready")}
                  onClick={() => void sendMessage()}
                  disabled={!canSend}
                  aria-label="Send"
                >
                  <span className="material-symbols-outlined">arrow_upward</span>
                </button>
              </div>
            </div>
          </div>
          <p className="chat-footer-note">Responses stream through your tproxy gateway.</p>
        </div>
      </div>
    </section>
  );
}
