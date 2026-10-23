import { useCallback, useEffect, useState } from "react";
import { isAutoApplyTool } from "../../cli-tools/constants";
import { fetchCLIToolStatus, type CLIToolStatus } from "./api";

/** Local install/config status of one auto-apply tool; null for tools the backend cannot inspect. */
export function useToolStatus(secret: string, toolId: string) {
  const [status, setStatus] = useState<CLIToolStatus | null>(null);
  const [loading, setLoading] = useState(() => isAutoApplyTool(toolId));

  const reload = useCallback(async () => {
    if (!isAutoApplyTool(toolId)) return;
    setLoading(true);
    try {
      setStatus(await fetchCLIToolStatus(secret, toolId));
    } catch (error) {
      setStatus({ installed: false, has_tproxy: false, has_9router: false, message: String(error) });
    } finally {
      setLoading(false);
    }
  }, [secret, toolId]);

  useEffect(() => {
    setStatus(null);
    void reload();
  }, [reload]);

  return { status, loading, reload };
}
