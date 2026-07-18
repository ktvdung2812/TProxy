import { memo, useEffect, useState } from "react";
import { Card } from "../ui";
import { fmt, fmtTime, recentRequestKey } from "./utils";
import type { UsageRecentRequest } from "./api";

type Props = {
  requests: UsageRecentRequest[];
};

function RecentRequestsTable({ requests }: Props) {
  const [timeTick, setTimeTick] = useState(0);

  useEffect(() => {
    const timer = window.setInterval(() => setTimeTick((value) => value + 1), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const items = requests ?? [];
  void timeTick;

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
            <colgroup>
              <col className="usage-recent-status-col" />
              <col className="usage-recent-model-col" />
              <col className="usage-recent-io-col" />
              <col className="usage-recent-when-col" />
            </colgroup>
            <thead>
              <tr>
                <th className="usage-recent-status-col" />
                <th>Model</th>
                <th className="usage-recent-io-col">In / Out</th>
                <th className="usage-recent-when-col">When</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => {
                const ok = !item.status || item.status === "ok" || item.status === "success" || item.status === "200";
                return (
                  <tr key={recentRequestKey(item)}>
                    <td>
                      <span className={`usage-status-dot ${ok ? "ok" : "error"}`} />
                    </td>
                    <td className="usage-recent-model" title={item.model}>{item.model}</td>
                    <td className="usage-recent-io-col">
                      <span className="usage-in">{fmt(item.promptTokens)}↑</span>{" "}
                      <span className="usage-out">{fmt(item.completionTokens)}↓</span>
                    </td>
                    <td className="usage-recent-when-col">{fmtTime(item.timestamp)}</td>
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

export const RecentRequests = memo(RecentRequestsTable);
