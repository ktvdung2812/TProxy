import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useRef, useState } from "react";
import { discoverCredentialModels, type DiscoveredModel } from "../providers/api";
import { Button, cn } from "../ui";
import { sendAccountChat, type AccountChatMessage } from "./api";

type Props = {
  secret: string;
  credentialId: string;
  credentialEnabled: boolean;
};

type Turn = AccountChatMessage & {
  /** Populated on assistant turns so the operator can judge the account, not just the answer. */
  latencyMs?: number;
  tokens?: number;
  failed?: boolean;
};

export function AccountTestChat({ secret, credentialId, credentialEnabled }: Props) {
  const { t, i18n } = useTranslation();
  const [models, setModels] = useState<DiscoveredModel[]>([]);
  const [model, setModel] = useState("");
  const [modelsError, setModelsError] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const transcriptRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setModels([]);
    setModel("");
    setModelsError("");
    setTurns([]);
    discoverCredentialModels(secret, credentialId)
      .then((response) => {
        if (cancelled) return;
        const items = response.data || [];
        setModels(items);
        if (items.length > 0) setModel(items[0].id);
        else setModelsError(t("featureA.noAccountModels"));
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        setModelsError(cause instanceof Error ? cause.message : t("featureA.loadModelsFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, [secret, credentialId]);

  // Pin the transcript to the newest turn; a reply that lands off-screen reads
  // as no reply at all.
  useEffect(() => {
    const node = transcriptRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [turns, sending]);

  const send = useCallback(async () => {
    const prompt = draft.trim();
    if (!prompt || !model || sending) return;

    const history: AccountChatMessage[] = [
      ...turns.filter((turn) => !turn.failed).map((turn) => ({ role: turn.role, content: turn.content })),
      { role: "user", content: prompt },
    ];
    setTurns((current) => [...current, { role: "user", content: prompt }]);
    setDraft("");
    setSending(true);
    try {
      const result = await sendAccountChat(secret, credentialId, model, history);
      const tokens = (result.usage?.input_tokens || 0) + (result.usage?.output_tokens || 0);
      setTurns((current) => [
        ...current,
        result.ok
          ? {
              role: "assistant",
              content: result.content?.trim() || t("featureA.emptyResponse"),
              latencyMs: result.latency_ms,
              tokens: tokens || undefined,
            }
          : {
              role: "assistant",
              content: result.error || t("featureA.requestFailed"),
              latencyMs: result.latency_ms,
              failed: true,
            },
      ]);
    } catch (cause) {
      setTurns((current) => [
        ...current,
        {
          role: "assistant",
          content: cause instanceof Error ? cause.message : t("featureA.requestFailed"),
          failed: true,
        },
      ]);
    } finally {
      setSending(false);
    }
  }, [draft, model, sending, turns, secret, credentialId, t]);

  const disabled = !credentialEnabled || !model || sending;

  return (
    <aside className="account-test-chat">
      <div className="account-test-chat-head">
        <div className="account-test-chat-title">
          <span className="material-symbols-outlined">forum</span>
          <h5>{t("featureA.testChat")}</h5>
        </div>
        {turns.length > 0 ? (
          <button
            type="button"
            className="quota-tracker-icon-btn"
            onClick={() => setTurns([])}
            aria-label={t("featureA.clearConversation")}
            title={t("featureA.clearConversation")}
          >
            <span className="material-symbols-outlined">delete_sweep</span>
          </button>
        ) : null}
      </div>

      <select
        className="account-test-chat-model"
        value={model}
        onChange={(event) => setModel(event.target.value)}
        disabled={models.length === 0}
        aria-label={t("featureA.modelToTest")}
      >
        {models.length === 0 ? <option value="">{t("featureA.noModels")}</option> : null}
        {models.map((item) => (
          <option key={item.id} value={item.id}>
            {item.name || item.id}
          </option>
        ))}
      </select>

      <div className="account-test-chat-transcript" ref={transcriptRef}>
        {modelsError ? (
          <p className="account-test-chat-empty account-test-chat-error">{modelsError}</p>
        ) : turns.length === 0 ? (
          <p className="account-test-chat-empty">
            {t("featureA.testChatHint")}
          </p>
        ) : (
          turns.map((turn, index) => (
            <div
              key={index}
              className={cn(
                "account-test-chat-turn",
                `account-test-chat-turn-${turn.role}`,
                turn.failed && "account-test-chat-turn-failed",
              )}
            >
              <p>{turn.content}</p>
              {turn.role === "assistant" && turn.latencyMs !== undefined ? (
                <span className="account-test-chat-meta">
                  {t("featureA.chatLatency", { value: new Intl.NumberFormat(i18n.language).format(turn.latencyMs) })}{turn.tokens ? ` · ${t("featureA.chatTokens", { value: new Intl.NumberFormat(i18n.language).format(turn.tokens) })}` : ""}
                </span>
              ) : null}
            </div>
          ))
        )}
        {sending ? (
          <div className="account-test-chat-turn account-test-chat-turn-assistant">
            <span className="material-symbols-outlined animate-spin" role="status" aria-label={t("featureA.sending")}>progress_activity</span>
          </div>
        ) : null}
      </div>

      <div className="account-test-chat-compose">
        <textarea
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              void send();
            }
          }}
          placeholder={credentialEnabled ? t("featureA.askSomething") : t("featureA.accountDisabled")}
          rows={2}
          disabled={!credentialEnabled}
        />
        <Button variant="primary" size="sm" icon="send" onClick={() => void send()} disabled={disabled || !draft.trim()}>
          {t("featureA.send")}
        </Button>
      </div>
    </aside>
  );
}
