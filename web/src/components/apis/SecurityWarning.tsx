import type { ReactNode } from "react";
import { cn } from "../ui";

type Props = {
  message: ReactNode;
  /** Warnings are amber; "danger" is for states that expose the gateway. */
  tone?: "warning" | "danger";
  action?: ReactNode;
};

export function SecurityWarning({ message, tone = "warning", action }: Props) {
  return (
    <div className={cn("ap-warning", `cp-tone-${tone}`)} role="note">
      <span className="material-symbols-outlined" aria-hidden>{tone === "danger" ? "gpp_maybe" : "warning"}</span>
      <p>{message}</p>
      {action}
    </div>
  );
}
