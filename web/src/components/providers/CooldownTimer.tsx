import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** Compact countdown from an ISO timestamp, ported from 9router CooldownTimer.js.
 *  Re-renders every second while the cooldown is still active. */
export function CooldownTimer({ until, onExpire }: { until: string; onExpire?: () => void }) {
  const { t } = useTranslation();
  const [remaining, setRemaining] = useState(() => remainingMs(until));

  useEffect(() => {
    setRemaining(remainingMs(until));
    if (remainingMs(until) <= 0) {
      onExpire?.();
      return;
    }
    const timer = window.setInterval(() => {
      const ms = remainingMs(until);
      setRemaining(ms);
      if (ms <= 0) {
        window.clearInterval(timer);
        onExpire?.();
      }
    }, 1000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [until]);

  if (remaining <= 0) return null;

  const totalSeconds = Math.ceil(remaining / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const label = hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m ${seconds}s`;

  return (
    <span className="cooldown-timer" title={`${t("providers.cooldownUntil")} ${new Date(until).toLocaleString()}`}>
      <span className="material-symbols-outlined">timer</span>
      {label}
    </span>
  );
}

function remainingMs(until: string): number {
  const target = Date.parse(until);
  if (Number.isNaN(target)) return 0;
  return target - Date.now();
}
