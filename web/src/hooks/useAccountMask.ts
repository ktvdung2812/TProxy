import { useCallback, useSyncExternalStore } from "react";
import { isAccountMaskEnabled, setAccountMaskEnabled, subscribeAccountMask } from "../lib/accountMask";

/** Global "hide accounts" toggle — masks emails/labels/ids across the UI. */
export function useAccountMask() {
  const masked = useSyncExternalStore(subscribeAccountMask, isAccountMaskEnabled);
  const toggle = useCallback(() => setAccountMaskEnabled(!isAccountMaskEnabled()), []);
  return { masked, setMasked: setAccountMaskEnabled, toggle };
}
