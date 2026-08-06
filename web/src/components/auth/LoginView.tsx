import { FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, Input } from "../ui";

type Props = {
  onLogin: (secret: string) => Promise<void>;
};

export function LoginView({ onLogin }: Props) {
  const { t } = useTranslation();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      await onLogin(password);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("auth.loginFailed"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="login-shell">
      <Card pad="lg" className="login-card" elev>
        <div className="login-brand">
          <span className="login-brand-mark">
            <span className="material-symbols-outlined">hub</span>
          </span>
          <div>
            <p className="login-eyebrow">tproxy control center</p>
            <h1>{t("auth.login")}</h1>
          </div>
        </div>

        <p className="login-desc" dangerouslySetInnerHTML={{ __html: t("auth.defaultPassword") }} />

        <form className="login-form" onSubmit={(event) => void submit(event)}>
          <label className="login-field">
            <span>{t("auth.password")}</span>
            <Input
              type="password"
              autoComplete="current-password"
              placeholder="••••••••••••"
              value={password}
              disabled={submitting}
              onChange={(event) => setPassword(event.target.value)}
              autoFocus
            />
          </label>

          {error ? (
            <div className="login-error" role="alert">
              <span className="material-symbols-outlined">error</span>
              <span>{error}</span>
            </div>
          ) : null}

          <Button type="submit" variant="primary" size="md" className="login-submit" disabled={submitting || !password.trim()}>
            {submitting ? t("auth.authenticating") : t("auth.login")}
          </Button>
        </form>

        <p className="login-hint">{t("auth.sessionHint")}</p>
      </Card>
    </div>
  );
}
