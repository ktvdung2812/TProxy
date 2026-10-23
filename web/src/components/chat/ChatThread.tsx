import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { EmptyPanel, IconAction, StatusPill, Tag } from "../page-kit";
import { cn } from "../ui";
import { ChatMarkdown } from "./ChatMarkdown";
import type { ChatMessage } from "./types";
import { formatDuration, textValue } from "./utils";

export const PRESET_IDS = ["explain", "code", "summarize", "ping"] as const;
const PRESET_ICON: Record<(typeof PRESET_IDS)[number], string> = {
  explain: "lightbulb",
  code: "code",
  summarize: "summarize",
  ping: "network_ping",
};

type EmptyProps = {
  noModels: boolean;
  modelName?: string;
  hasKey: boolean;
  onOpenPicker: () => void;
  onOpenSettings: () => void;
  onPreset: (prompt: string) => void;
};

export function ChatEmptyState({ noModels, modelName, hasKey, onOpenPicker, onOpenSettings, onPreset }: EmptyProps) {
  const { t } = useTranslation();
  if (noModels) {
    return (
      <EmptyPanel
        icon="smart_toy"
        title={t("chat.noModelsTitle")}
        description={t("chat.noModelsAvailable")}
        action={
          <div className="ch-inline-links">
            <Link to="/providers">{t("chat.openProviders")}</Link>
            <Link to="/models">{t("chat.openModels")}</Link>
          </div>
        }
      />
    );
  }
  return (
    <div className="ch-empty">
      <span className="material-symbols-outlined ch-empty-icon" aria-hidden>forum</span>
      <h2>{t("chat.startConversation")}</h2>
      <p>{t("chat.chatDescription")}</p>

      <div className="ch-ready" role="list" aria-label={t("chat.readiness")}>
        <button type="button" role="listitem" className="ch-ready-item cp-tone-success" onClick={onOpenPicker}>
          <span className="material-symbols-outlined" aria-hidden>check_circle</span>
          <span>{t("chat.readyModel")}</span>
          <strong>{modelName}</strong>
        </button>
        <button
          type="button"
          role="listitem"
          className={cn("ch-ready-item", hasKey ? "cp-tone-success" : "cp-tone-warning")}
          onClick={onOpenSettings}
        >
          <span className="material-symbols-outlined" aria-hidden>{hasKey ? "check_circle" : "key_off"}</span>
          <span>{t("chat.clientApiKey")}</span>
          <strong>{hasKey ? t("chat.keyReady") : t("chat.keyMissingAction")}</strong>
        </button>
      </div>

      <div className="ch-presets">
        {PRESET_IDS.map((id) => (
          <button key={id} type="button" className="ch-preset" onClick={() => onPreset(t(`chat.preset.${id}.prompt`))}>
            <span className="material-symbols-outlined" aria-hidden>{PRESET_ICON[id]}</span>
            <strong>{t(`chat.preset.${id}.label`)}</strong>
            <small>{t(`chat.preset.${id}.prompt`)}</small>
          </button>
        ))}
      </div>
    </div>
  );
}

type ThreadProps = {
  messages: ChatMessage[];
  streamingMessageId: string;
  fallbackModelName: string;
  canRegenerate: boolean;
  onRegenerate: () => void;
};

export const ChatThread = forwardRef<HTMLDivElement, ThreadProps>(function ChatThread(
  { messages, streamingMessageId, fallbackModelName, canRegenerate, onRegenerate },
  endRef,
) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const lastAssistantId = [...messages].reverse().find((message) => message.role === "assistant")?.id;

  return (
    <div className="ch-thread">
      {messages.map((message) => {
        const isUser = message.role === "user";
        const content = textValue(message.content);
        const streaming = !isUser && message.id === streamingMessageId && message.status === "streaming";
        const isError = message.status === "error";
        const isLast = message.id === lastAssistantId;
        const timing = [
          message.firstTokenMs !== undefined ? t("chat.firstToken", { time: formatDuration(message.firstTokenMs) }) : "",
          message.durationMs !== undefined ? t("chat.totalTime", { time: formatDuration(message.durationMs) }) : "",
        ].filter(Boolean).join(" · ");

        if (isUser) {
          return (
            <div key={message.id} className="ch-msg is-user">
              {message.attachments?.length ? (
                <div className="ch-attachments">
                  {message.attachments.map((attachment) => (
                    <a key={attachment.id} href={attachment.dataUrl} target="_blank" rel="noreferrer" className="ch-attachment">
                      <img src={attachment.dataUrl} alt={attachment.name} />
                    </a>
                  ))}
                </div>
              ) : null}
              {content ? <div className="ch-bubble">{content}</div> : null}
              <div className="ch-msg-actions">
                <IconAction
                  icon={copied === message.id ? "check" : "content_copy"}
                  label={copied === message.id ? t("common.copied") : t("chat.copyMessage")}
                  onClick={() => copy(content, message.id)}
                />
              </div>
            </div>
          );
        }

        return (
          <div key={message.id} className={cn("ch-msg is-assistant", isError && "is-error")}>
            <div className="ch-msg-head">
              <span className="material-symbols-outlined ch-msg-avatar" aria-hidden>smart_toy</span>
              <strong>{message.modelName || fallbackModelName || t("chat.assistant")}</strong>
              {streaming ? <StatusPill tone="info" loading>{t("chat.streaming")}</StatusPill> : null}
              {isError ? <StatusPill tone="danger">{t("chat.failed")}</StatusPill> : null}
              {message.stopped ? <Tag tone="muted">{t("chat.stopped")}</Tag> : null}
              {timing && !streaming ? <span className="ch-msg-timing">{timing}</span> : null}
            </div>
            <div className="ch-msg-body">
              {isError ? (
                <p className="ch-msg-error">
                  <span className="material-symbols-outlined" aria-hidden>error</span>
                  <span>{content.replace(/^Error:\s*/, "")}</span>
                </p>
              ) : (
                <ChatMarkdown content={content} streaming={streaming} />
              )}
            </div>
            {!streaming ? (
              <div className="ch-msg-actions">
                {content ? (
                  <IconAction
                    icon={copied === message.id ? "check" : "content_copy"}
                    label={copied === message.id ? t("common.copied") : t("chat.copyMessage")}
                    onClick={() => copy(content, message.id)}
                  />
                ) : null}
                {isLast && canRegenerate ? (
                  <IconAction icon="refresh" label={isError ? t("chat.retry") : t("chat.regenerate")} onClick={onRegenerate} />
                ) : null}
              </div>
            ) : null}
          </div>
        );
      })}
      <div ref={endRef} />
    </div>
  );
});
