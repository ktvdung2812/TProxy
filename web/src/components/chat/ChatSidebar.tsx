import { useMemo, useState } from "react";
import type { KeyboardEvent, MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { ActionMenu, IconAction } from "../page-kit";
import { Button, ConfirmDialog, cn } from "../ui";
import type { ChatSession } from "./types";
import { formatRelativeTime, textValue } from "./utils";

type Props = {
  sessions: ChatSession[];
  activeSessionId: string;
  canCreate: boolean;
  onNewChat: () => void;
  onSelect: (sessionId: string) => void;
  onDelete: (sessionId: string) => void;
  onDeleteAll: () => void;
  onCollapse: () => void;
};

export function ChatSidebar({ sessions, activeSessionId, canCreate, onNewChat, onSelect, onDelete, onDeleteAll, onCollapse }: Props) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const [pendingDelete, setPendingDelete] = useState<ChatSession | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);

  const items = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return [...sessions]
      .sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime())
      .filter((session) => {
        if (!needle) return true;
        const text = [session.title, session.modelName, ...session.messages.map((message) => textValue(message.content))]
          .join(" ")
          .toLowerCase();
        return text.includes(needle);
      });
  }, [sessions, query]);

  const open = (sessionId: string) => (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
    event.preventDefault();
    onSelect(sessionId);
  };
  const askDelete = (session: ChatSession) => (event: MouseEvent) => {
    event.stopPropagation();
    // An untouched chat has nothing to lose, so it goes without a prompt.
    if (session.messages.length === 0) onDelete(session.id);
    else setPendingDelete(session);
  };

  return (
    <aside className="ch-sidebar" aria-label={t("chat.history")}>
      <div className="ch-sidebar-head">
        <Button variant="outline" size="sm" icon="add" block onClick={onNewChat} disabled={!canCreate}>
          {t("chat.newChat")}
        </Button>
        <ActionMenu
          label={t("chat.historyActions")}
          items={[
            {
              key: "delete-all",
              icon: "delete_sweep",
              label: t("chat.deleteAll"),
              hint: t("chat.deleteAllHint", { count: sessions.length }),
              danger: true,
              disabled: sessions.length === 0,
              onSelect: () => setConfirmAll(true),
            },
          ]}
        />
        <IconAction icon="left_panel_close" label={t("chat.collapseHistory")} onClick={onCollapse} />
      </div>
      <label className="ch-sidebar-search">
        <span className="material-symbols-outlined" aria-hidden>search</span>
        <input
          type="search"
          value={query}
          placeholder={t("chat.searchHistory")}
          aria-label={t("chat.searchHistory")}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && query) {
              event.stopPropagation();
              setQuery("");
            }
          }}
        />
      </label>

      <div className="ch-session-list custom-scrollbar">
        {items.length === 0 ? (
          <p className="ch-session-empty">{query ? t("chat.noHistoryMatch") : t("chat.noConversations")}</p>
        ) : (
          items.map((session) => {
            const active = session.id === activeSessionId;
            const lastUser = [...session.messages].reverse().find((message) => message.role === "user");
            const failed = session.messages.some((message) => message.status === "error");
            const untouched = session.messages.length === 0;
            return (
              <div
                key={session.id}
                role="button"
                tabIndex={0}
                aria-current={active ? "true" : undefined}
                className={cn("ch-session", active && "is-active", failed && "has-error")}
                onClick={() => onSelect(session.id)}
                onKeyDown={open(session.id)}
              >
                <span className="ch-session-title">{untouched ? t("chat.newChat") : session.title}</span>
                <span className="ch-session-preview">{textValue(lastUser?.content) || t("chat.emptyChat")}</span>
                <span className="ch-session-meta">
                  <span>{session.modelName}</span>
                  <span>{formatRelativeTime(session.updatedAt, t("chat.now"))}</span>
                </span>
                <IconAction
                  icon="delete"
                  label={t("chat.deleteChat")}
                  className="ch-session-delete"
                  danger
                  onClick={askDelete(session)}
                  onKeyDown={(event) => event.stopPropagation()}
                />
              </div>
            );
          })
        )}
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        title={t("chat.deleteConfirmTitle")}
        message={t("chat.deleteConfirmMessage", { title: pendingDelete?.title || "" })}
        confirmText={t("common.delete")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => setPendingDelete(null)}
        onConfirm={() => {
          if (pendingDelete) onDelete(pendingDelete.id);
          setPendingDelete(null);
        }}
      />
      <ConfirmDialog
        open={confirmAll}
        title={t("chat.deleteAllConfirmTitle")}
        message={t("chat.deleteAllConfirmMessage", { count: sessions.length })}
        confirmText={t("chat.deleteAll")}
        cancelText={t("common.cancel")}
        variant="danger"
        onClose={() => setConfirmAll(false)}
        onConfirm={() => {
          setConfirmAll(false);
          onDeleteAll();
        }}
      />
    </aside>
  );
}
