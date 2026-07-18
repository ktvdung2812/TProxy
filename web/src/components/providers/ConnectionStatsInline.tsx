import { useEffect, useState } from "react";
import { Badge } from "../ui";
import { getProviderStats, type Credential } from "./types";

type Props = {
  credentials: Credential[];
};

/** Active / error account counts for the Connections card header. */
export function ConnectionStatsInline({ credentials }: Props) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const interval = window.setInterval(() => setTick((value) => value + 1), 30000);
    return () => window.clearInterval(interval);
  }, []);

  if (credentials.length === 0) {
    return null;
  }

  const stats = getProviderStats(credentials);

  return (
    <span className="connection-stats-inline">
      <Badge variant={stats.connected > 0 ? "success" : "default"} size="sm" dot>
        {stats.connected} active
      </Badge>
      {stats.error > 0 ? (
        <Badge variant="error" size="sm" dot>
          {stats.error} error
        </Badge>
      ) : null}
    </span>
  );
}
