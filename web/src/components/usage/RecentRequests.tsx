import { useEffect, useState } from "react";
import { Card } from "../ui";
import { fmt, fmtTime } from "./utils";
import type { UsageRecentRequest } from "./api";

type Props = {
  requests: UsageRecentRequest[];
};

function TimeAgo({ timestamp }: { timestamp: string }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTick((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, []);
  return <>{fmtTime(timestamp)}</>;
}

export function RecentRequests({ requests = [] }: Props) {
  const items = requests ?? [];
  return (
    <Card pad="sm" className="usage-recent-card">
      <div className="usage-recent-head">
        <span>Recent Requests</span>
      </div>
      {!items.length ? (
        <div className="usage-recent-empty">No requests yet.</div>
      ) : (
        <div className="usage-recent-scroll custom-scrollbar">
          <table className="usage-recent-table">
            <thead>
              <tr>
                <th className="usage-recent-status-col" />
                <th>Model</th>
                <th className="usage-recent-io-col">In / Out</th>
                <th className="usage-recent-when-col">When</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item, index) => {
                const ok = !item.status || item.status === "ok" || item.status === "success" || item.status === "200";
                return (
                  <tr key={`${item.timestamp}-${item.model}-${index}`}>
                    <td>
                      <span className={`usage-status-dot ${ok ? "ok" : "error"}`} />
                    </td>
                    <td className="usage-recent-model" title={item.model}>{item.model}</td>
                    <td className="usage-recent-io-col">
                      <span className="usage-in">{fmt(item.promptTokens)}↑</span>{" "}
                      <span className="usage-out">{fmt(item.completionTokens)}↓</span>
                    </td>
                    <td className="usage-recent-when-col">
                      <TimeAgo timestamp={item.timestamp} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
