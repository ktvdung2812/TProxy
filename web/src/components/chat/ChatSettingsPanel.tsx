import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useApiKeySecrets } from "../../hooks/useApiKeySecrets";
import { getStoredApiKeySecret, maskApiKeySecret } from "../../lib/apiKeySecrets";
import { IconAction, LinkButton } from "../page-kit";
import { cn } from "../ui";
import type { ChatParams } from "./types";
import { modKeyLabel } from "./utils";

export type ChatApiKeyOption = { id: string; name?: string; enabled: boolean };

/** Enabled API keys whose secret this browser has saved, in snapshot order. */
export function useUsableApiKeys(apiKeys: ChatApiKeyOption[]) {
  useApiKeySecrets();
  return apiKeys
    .filter((key) => key.enabled)
    .map((key) => ({ ...key, secret: getStoredApiKeySecret(key.id) || "" }))
    .filter((key) => key.secret);
}

type Props = {
  apiKeys: ChatApiKeyOption[];
  apiKey: string;
  onApiKeyChange: (value: string) => void;
  params: ChatParams;
  onParamsChange: (next: ChatParams) => void;
  onClose: () => void;
};

export function ChatSettingsPanel({ apiKeys, apiKey, onApiKeyChange, params, onParamsChange, onClose }: Props) {
  const { t } = useTranslation();
  const usable = useUsableApiKeys(apiKeys);
  const [reveal, setReveal] = useState(false);
  const trimmed = apiKey.trim();
  const matched = usable.find((key) => key.secret === trimmed);
  const withoutSecret = apiKeys.filter((key) => key.enabled).length - usable.length;
  const set = (patch: Partial<ChatParams>) => onParamsChange({ ...params, ...patch });
  const paramsSet = Boolean(params.systemPrompt.trim() || params.temperature.trim() || params.maxTokens.trim());

  return (
    <aside className="ch-panel custom-scrollbar" aria-label={t("chat.settingsTitle")}>
      <header className="ch-panel-head">
        <strong>{t("chat.settingsTitle")}</strong>
        <IconAction icon="close" label={t("chat.close")} onClick={onClose} />
      </header>

      <section className="ch-panel-section">
        <h4>{t("chat.clientApiKey")}</h4>
        <p className="ch-panel-hint">
          <Trans i18nKey="chat.apiKeyHint" components={{ code: <code />, link: <Link to="/apis" /> }} />
        </p>
        {usable.length > 0 ? (
          <div className="ch-key-list" role="radiogroup" aria-label={t("chat.clientApiKey")}>
            {usable.map((key) => {
              const selected = key.secret === trimmed;
              return (
                <button
                  key={key.id}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  className={cn("ch-key", selected && "is-selected")}
                  onClick={() => onApiKeyChange(key.secret)}
                >
                  <span className="material-symbols-outlined" aria-hidden>{selected ? "radio_button_checked" : "radio_button_unchecked"}</span>
                  <span className="ch-key-copy">
                    <strong>{key.name || key.id}</strong>
                    <code>{maskApiKeySecret(key.secret)}</code>
                  </span>
                </button>
              );
            })}
          </div>
        ) : null}
        {withoutSecret > 0 ? (
          <p className="ch-panel-hint">{t("chat.keysWithoutSecret", { count: withoutSecret })}</p>
        ) : null}

        <label className="ch-field">
          <span>{matched || !trimmed ? t("chat.customKey") : t("chat.customKeyInUse")}</span>
          <div className="ch-key-input">
            <input
              className="input"
              type={reveal ? "text" : "password"}
              autoComplete="off"
              spellCheck={false}
              value={matched ? "" : apiKey}
              placeholder={t("chat.tproxyApiKeyPlaceholder")}
              onChange={(event) => onApiKeyChange(event.target.value)}
            />
            <IconAction
              icon={reveal ? "visibility_off" : "visibility"}
              label={reveal ? t("chat.hideKey") : t("chat.showKey")}
              bordered
              onClick={() => setReveal((value) => !value)}
            />
          </div>
        </label>
        {trimmed ? (
          <LinkButton onClick={() => onApiKeyChange("")}>{t("chat.forgetKey")}</LinkButton>
        ) : null}
      </section>

      <section className="ch-panel-section">
        <div className="ch-panel-section-head">
          <h4>{t("chat.parameters")}</h4>
          {paramsSet ? (
            <LinkButton onClick={() => onParamsChange({ systemPrompt: "", temperature: "", maxTokens: "" })}>
              {t("chat.resetParameters")}
            </LinkButton>
          ) : null}
        </div>
        <p className="ch-panel-hint">{t("chat.parametersHint")}</p>
        <label className="ch-field">
          <span>{t("chat.systemPrompt")}</span>
          <textarea
            className="textarea"
            rows={4}
            value={params.systemPrompt}
            placeholder={t("chat.systemPromptPlaceholder")}
            onChange={(event) => set({ systemPrompt: event.target.value })}
          />
        </label>
        <div className="ch-field-pair">
          <label className="ch-field">
            <span>{t("chat.temperature")}</span>
            <input
              className="input"
              type="number"
              min={0}
              max={2}
              step={0.1}
              inputMode="decimal"
              value={params.temperature}
              placeholder={t("chat.modelDefault")}
              onChange={(event) => set({ temperature: event.target.value })}
            />
          </label>
          <label className="ch-field">
            <span>{t("chat.maxTokens")}</span>
            <input
              className="input"
              type="number"
              min={1}
              step={1}
              inputMode="numeric"
              value={params.maxTokens}
              placeholder={t("chat.modelDefault")}
              onChange={(event) => set({ maxTokens: event.target.value })}
            />
          </label>
        </div>
      </section>

      <section className="ch-panel-section">
        <h4>{t("chat.shortcuts")}</h4>
        <dl className="ch-shortcuts">
          <div><dt><kbd className="cp-kbd">↵</kbd></dt><dd>{t("chat.shortcutSend")}</dd></div>
          <div><dt><kbd className="cp-kbd">⇧ ↵</kbd></dt><dd>{t("chat.shortcutNewline")}</dd></div>
          <div><dt><kbd className="cp-kbd">Esc</kbd></dt><dd>{t("chat.shortcutStop")}</dd></div>
          <div><dt><kbd className="cp-kbd">{modKeyLabel()} K</kbd></dt><dd>{t("chat.shortcutModel")}</dd></div>
        </dl>
      </section>
    </aside>
  );
}
