import { forwardRef, useLayoutEffect, useRef } from "react";
import type { ChangeEvent, KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { IconAction } from "../page-kit";
import { cn } from "../ui";
import type { ChatAttachment } from "./types";

type Props = {
  draft: string;
  onDraftChange: (value: string) => void;
  attachments: ChatAttachment[];
  onAttach: (event: ChangeEvent<HTMLInputElement>) => void;
  onRemoveAttachment: (id: string) => void;
  canSend: boolean;
  isSending: boolean;
  disabled: boolean;
  /** Why sending is blocked (no key/model), shown instead of the shortcut hint. */
  blockedReason?: string;
  modelName?: string;
  paramsSummary?: string;
  onSend: () => void;
  onStop: () => void;
  onOpenPicker: () => void;
  onOpenSettings: () => void;
};

const MAX_HEIGHT = 240;

export const ChatComposer = forwardRef<HTMLTextAreaElement, Props>(function ChatComposer(
  {
    draft,
    onDraftChange,
    attachments,
    onAttach,
    onRemoveAttachment,
    canSend,
    isSending,
    disabled,
    blockedReason,
    modelName,
    paramsSummary,
    onSend,
    onStop,
    onOpenPicker,
    onOpenSettings,
  },
  textareaRef,
) {
  const { t } = useTranslation();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const localRef = useRef<HTMLTextAreaElement | null>(null);

  // Grow with the text up to MAX_HEIGHT, then scroll.
  useLayoutEffect(() => {
    const element = localRef.current;
    if (!element) return;
    element.style.height = "auto";
    element.style.height = `${Math.min(element.scrollHeight, MAX_HEIGHT)}px`;
  }, [draft]);

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      if (canSend) onSend();
    }
  };

  return (
    <div className="ch-composer-wrap">
      <div className={cn("ch-composer", isSending && "is-busy")}>
        {attachments.length > 0 ? (
          <div className="ch-composer-files">
            {attachments.map((attachment) => (
              <span key={attachment.id} className="ch-composer-file">
                <img src={attachment.dataUrl} alt="" />
                <span>{attachment.name}</span>
                <button type="button" onClick={() => onRemoveAttachment(attachment.id)} aria-label={t("chat.removeAttachment")}>
                  <span className="material-symbols-outlined" aria-hidden>close</span>
                </button>
              </span>
            ))}
          </div>
        ) : null}
        <textarea
          ref={(element) => {
            localRef.current = element;
            if (typeof textareaRef === "function") textareaRef(element);
            else if (textareaRef) textareaRef.current = element;
          }}
          value={draft}
          onChange={(event) => onDraftChange(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t("chat.messageAI")}
          aria-label={t("chat.messageAI")}
          rows={1}
          className="ch-composer-input custom-scrollbar"
        />
        <div className="ch-composer-bar">
          <IconAction icon="attach_file" label={t("chat.attachImage")} disabled={disabled} onClick={() => fileInputRef.current?.click()} />
          <input
            ref={fileInputRef}
            type="file"
            accept="image/*"
            multiple
            className="sr-only"
            tabIndex={-1}
            onChange={onAttach}
          />
          <button type="button" className="ch-chip" onClick={onOpenPicker} disabled={disabled} title={t("chat.changeModel")}>
            <span className="material-symbols-outlined" aria-hidden>smart_toy</span>
            <span>{modelName || t("chat.noModel")}</span>
          </button>
          {paramsSummary ? (
            <button type="button" className="ch-chip" onClick={onOpenSettings} title={t("chat.parameters")}>
              <span className="material-symbols-outlined" aria-hidden>tune</span>
              <span>{paramsSummary}</span>
            </button>
          ) : null}
          <span className={cn("ch-composer-hint", blockedReason && "is-blocked")}>
            {blockedReason || t("chat.composerHint")}
          </span>
          {isSending ? (
            <button type="button" className="ch-send is-stop" onClick={onStop} aria-label={t("chat.stop")} title={`${t("chat.stop")} (Esc)`}>
              <span className="material-symbols-outlined" aria-hidden>stop</span>
              <span>{t("chat.stop")}</span>
              <kbd>Esc</kbd>
            </button>
          ) : (
            <button
              type="button"
              className={cn("ch-send", canSend && "is-ready")}
              onClick={onSend}
              disabled={!canSend}
              aria-label={t("chat.send")}
              title={`${t("chat.send")} (Enter)`}
            >
              <span className="material-symbols-outlined" aria-hidden>arrow_upward</span>
              <span>{t("chat.send")}</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
});
