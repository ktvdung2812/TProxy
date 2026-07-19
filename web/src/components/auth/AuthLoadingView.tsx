export function AuthLoadingView() {
  return (
    <div className="login-shell">
      <div className="login-loading" role="status" aria-live="polite">
        <span className="material-symbols-outlined login-loading-icon">hub</span>
        <p>Đang xác thực phiên…</p>
      </div>
    </div>
  );
}
