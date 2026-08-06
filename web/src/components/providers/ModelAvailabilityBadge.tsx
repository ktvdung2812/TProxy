import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "../ui";
import { isOnCooldown, type Credential } from "./types";

type Props = {
  credentials: Credential[];
};

/**
 * Live availability badge for a provider's credentials.
 *
 * Adaptation note: 9router polls `GET /api/models/availability` every 30s and
 * offers a "Clear cooldown" action. tdproxy has no such endpoint; cooldowns are
 * driven by `credential.cooldown_until` (already present in the snapshot) and
 * expire automatically server-side. So this component re-evaluates the passed
 * credentials every 30s (so the badge flips back to healthy without a full
 * snapshot reload) and surfaces the unhealthy count. No clear action is offered
 * because there is no backend endpoint to force-clear a tdproxy cooldown.
 */
export function ModelAvailabilityBadge({ credentials }: Props) {
  const { t } = useTranslation();
  // Tick every 30s so cooldown expiry is reflected even without a snapshot refetch.
  const [, setTick] = useState(0);
  useEffect(() => {
    const interval = window.setInterval(() => setTick((t) => t + 1), 30000);
    return () => window.clearInterval(interval);
  }, []);

  let cooldown = 0;
  let authRequired = 0;
  for (const c of credentials) {
    if (!c.enabled) continue;
    if (isOnCooldown(c.cooldown_until)) cooldown++;
    else if (c.status === "auth_required") authRequired++;
  }
  const unhealthy = cooldown + authRequired;

  if (unhealthy === 0) {
    return <Badge variant="success" size="sm" dot>{t("providers.allAvailable")}</Badge>;
  }
  if (cooldown > 0 && authRequired === 0) {
    return <Badge variant="warning" size="sm" dot>{cooldown} {t("providers.cooldownBadge")}</Badge>;
  }
  if (authRequired > 0 && cooldown === 0) {
    return <Badge variant="error" size="sm" dot>{authRequired} {t("providers.authRequiredBadge")}</Badge>;
  }
  return <Badge variant="error" size="sm" dot>{unhealthy} {t("providers.unavailableBadge")}</Badge>;
}
