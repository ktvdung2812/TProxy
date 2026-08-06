import { useTranslation } from "react-i18next";

export function AuthLoadingView() {
  const { t } = useTranslation();
  return (
    <div className="login-shell">
      <div className="login-loading" role="status" aria-live="polite">
        <span className="material-symbols-outlined login-loading-icon">hub</span>
        <p>{t("auth.checkingSession")}</p>
      </div>
    </div>
  );
}
