import { useEffect, useState } from "react";
import { CLI_TOOLS, isAutoApplyTool, MITM_TOOLS } from "../../cli-tools/constants";
import { fetchCLIToolStatuses, type CLIToolStatus } from "./api";
import { MitmToolCard } from "./MitmToolCard";
import { ToolSummaryCard } from "./ToolSummaryCard";

type Props = {
  secret: string;
};

export function CLIToolsView({ secret }: Props) {
  const regularTools = Object.entries(CLI_TOOLS);
  const mitmTools = Object.entries(MITM_TOOLS);
  const [statuses, setStatuses] = useState<Record<string, CLIToolStatus>>({});

  useEffect(() => {
    let cancelled = false;
    void fetchCLIToolStatuses(secret)
      .then((next) => {
        if (!cancelled) setStatuses(next);
      })
      .catch(() => {
        if (!cancelled) setStatuses({});
      });
    return () => {
      cancelled = true;
    };
  }, [secret]);

  return (
    <section className="section cli-tools-section">
      <div className="section-head">
        <div>
          <p className="eyebrow">External clients</p>
          <h2>CLI Tools</h2>
          <p>Connect coding CLIs and IDE extensions to tproxy&apos;s stable virtual models.</p>
        </div>
        <span className="meta">{regularTools.length} tools</span>
      </div>

      <div className="cli-tools-grid">
        {regularTools.map(([toolId, tool]) => (
          <ToolSummaryCard
            key={toolId}
            toolId={toolId}
            tool={tool}
            status={isAutoApplyTool(toolId) ? statuses[toolId] : undefined}
          />
        ))}
      </div>

      <div className="cli-tools-mitm">
        <div className="cli-tools-mitm-head">
          <span className="material-symbols-outlined">security</span>
          <h3>MITM Tools</h3>
        </div>
        <p className="cli-tool-hint" style={{ marginBottom: 12 }}>
          <strong>Not supported in tproxy.</strong> 9Router-style MITM/pxpipe capture is intentionally out of scope.
          Connect these products via <strong>Providers</strong> OAuth/API key instead (Antigravity, GitHub Copilot, Kiro, …).
          Domain labels below are for reference only.
        </p>
        <div className="cli-tools-grid">
          {mitmTools.map(([toolId, tool]) => (
            <MitmToolCard key={toolId} toolId={toolId} tool={tool} />
          ))}
        </div>
      </div>
    </section>
  );
}
