import { FormEvent, useState } from "react";
import { Button, Card, Input } from "../ui";

type Props = {
  onLogin: (secret: string) => Promise<void>;
};

export function LoginView({ onLogin }: Props) {
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
      setError(cause instanceof Error ? cause.message : "Đăng nhập thất bại");
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
            <h1>Đăng nhập</h1>
          </div>
        </div>

        <p className="login-desc">
          Mật khẩu mặc định là <code>123123</code>, đổi mật khẩu trong <code>/settings</code>.
        </p>

        <form className="login-form" onSubmit={(event) => void submit(event)}>
          <label className="login-field">
            <span>Mật khẩu</span>
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
            {submitting ? "Đang xác thực…" : "Đăng nhập"}
          </Button>
        </form>

        <p className="login-hint">
          Phiên đăng nhập được lưu trên trình duyệt này. Chỉ bạn có quyền truy cập — không chia sẻ secret.
        </p>
      </Card>
    </div>
  );
}
