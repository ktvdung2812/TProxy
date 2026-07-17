import { CLI_TOOLS, MITM_TOOLS } from "../../cli-tools/constants";
import { MitmToolCard } from "./MitmToolCard";
import { ToolSummaryCard } from "./ToolSummaryCard";

export function CLIToolsView() {
  const regularTools = Object.entries(CLI_TOOLS);
  const mitmTools = Object.entries(MITM_TOOLS);

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
          <ToolSummaryCard key={toolId} toolId={toolId} tool={tool} />
        ))}
      </div>

      <div className="cli-tools-mitm">
        <div className="cli-tools-mitm-head">
          <span className="material-symbols-outlined">security</span>
          <h3>MITM Tools</h3>
        </div>
        <div className="cli-tools-grid">
          {mitmTools.map(([toolId, tool]) => (
            <MitmToolCard key={toolId} toolId={toolId} tool={tool} />
          ))}
        </div>
      </div>
    </section>
  );
}
