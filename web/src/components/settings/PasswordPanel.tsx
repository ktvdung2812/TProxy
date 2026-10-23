import { useState } from "react";
import type { FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { IconAction } from "../page-kit";
import { Button } from "../ui";
import { changeDashboardPassword } from "./api";
import { SettingRow, SettingsPanel } from "./SettingsParts";

const MIN_LENGTH = 6;

type Props = {
  secret: string;
  onError: (message: string) => void;
  onNotice?: (message: string) => void;
  onPasswordChanged?: (newPassword: string) => void;
};

/** Its own form with its own submit: a password change is never part of the page-wide save. */
export function PasswordPanel({ secret, onError, onNotice, onPasswordChanged }: Props) {
  const { t } = useTranslation();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [visible, setVisible] = useState(false);
  const [saving, setSaving] = useState(false);

  const tooShort = newPassword.length > 0 && newPassword.length < MIN_LENGTH;
  const mismatch = confirmPassword.length > 0 && newPassword !== confirmPassword;
  const filled = Boolean(currentPassword && newPassword && confirmPassword);
  const started = Boolean(currentPassword || newPassword || confirmPassword);

  const clear = () => {
    setCurrentPassword("");
    setNewPassword("");
    setConfirmPassword("");
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (newPassword !== confirmPassword) {
      onError(t("settings.page.passwordMismatch"));
      return;
    }
    if (newPassword.length < MIN_LENGTH) {
      onError(t("settings.page.passwordTooShort", { count: MIN_LENGTH }));
      return;
    }
    setSaving(true);
    try {
      await changeDashboardPassword(secret, currentPassword, newPassword);
      clear();
      onPasswordChanged?.(newPassword);
      onNotice?.(t("settings.page.passwordChanged"));
    } catch (error) {
      onError(error instanceof Error ? error.message : t("settings.page.passwordFailed"));
    } finally {
      setSaving(false);
    }
  };

  const type = visible ? "text" : "password";

  return (
    <SettingsPanel
      id="password"
      icon="lock"
      title={t("settings.page.passwordTitle")}
      subtitle={t("settings.page.passwordSubtitle")}
      actions={
        <IconAction
          icon={visible ? "visibility_off" : "visibility"}
          label={visible ? t("settings.page.hidePasswords") : t("settings.page.showPasswords")}
          bordered
          active={visible}
          onClick={() => setVisible((value) => !value)}
        />
      }
    >
      <form className="st-password" onSubmit={(event) => void submit(event)} autoComplete="on">
        <p className="st-muted">
          <Trans i18nKey="settings.page.passwordIntro" components={{ code: <code /> }} />
        </p>
        <SettingRow label={t("settings.page.currentPassword")} htmlFor="st-pw-current">
          <input
            id="st-pw-current"
            className="input"
            type={type}
            autoComplete="current-password"
            value={currentPassword}
            disabled={saving}
            onChange={(event) => setCurrentPassword(event.target.value)}
          />
        </SettingRow>
        <SettingRow
          label={t("settings.page.newPassword")}
          hint={t("settings.page.passwordRule", { count: MIN_LENGTH })}
          htmlFor="st-pw-new"
          error={tooShort ? t("settings.page.passwordTooShort", { count: MIN_LENGTH }) : undefined}
        >
          <input
            id="st-pw-new"
            className="input"
            type={type}
            autoComplete="new-password"
            value={newPassword}
            disabled={saving}
            aria-invalid={tooShort}
            onChange={(event) => setNewPassword(event.target.value)}
          />
        </SettingRow>
        <SettingRow
          label={t("settings.page.confirmPassword")}
          htmlFor="st-pw-confirm"
          error={mismatch ? t("settings.page.passwordMismatch") : undefined}
        >
          <input
            id="st-pw-confirm"
            className="input"
            type={type}
            autoComplete="new-password"
            value={confirmPassword}
            disabled={saving}
            aria-invalid={mismatch}
            onChange={(event) => setConfirmPassword(event.target.value)}
          />
        </SettingRow>
        <div className="st-form-actions">
          {started ? (
            <Button type="button" variant="ghost" size="sm" disabled={saving} onClick={clear}>
              {t("settings.page.clearFields")}
            </Button>
          ) : null}
          <Button
            type="submit"
            variant="secondary"
            size="sm"
            icon="key"
            loading={saving}
            disabled={!filled || tooShort || mismatch}
          >
            {saving ? t("settings.page.saving") : t("settings.page.changePassword")}
          </Button>
        </div>
      </form>
    </SettingsPanel>
  );
}
