import { Link } from "react-router-dom";
import { Badge, Card } from "../ui";
import type { CLITool } from "../../cli-tools/constants";
import type { CLIToolStatus } from "./api";

type Props = {
  toolId: string;
  tool: CLITool;
  status?: CLIToolStatus;
};

export function ToolSummaryCard({ toolId, tool, status }: Props) {
  const connected = status?.has_tproxy || status?.has_9router;

  return (
    <Link to={`/cli-tools/${toolId}`} className="cli-tool-link">
      <Card pad="sm" className="cli-tool-card">
        <div className="cli-tool-card-head">
          <span className="cli-tool-icon" style={{ color: tool.color }}>
            <span className="material-symbols-outlined">{tool.icon}</span>
          </span>
          <div className="cli-tool-card-body">
            <div className="cli-tool-title-row">
              <h3>{tool.name}</h3>
              {connected ? (
                <Badge variant="success" size="sm">
                  Connected
                </Badge>
              ) : status?.installed ? (
                <Badge variant="warning" size="sm">
                  Not configured
                </Badge>
              ) : null}
            </div>
            <span className="cli-tool-type">{tool.configType}</span>
          </div>
          <span className="material-symbols-outlined cli-tool-chevron">chevron_right</span>
        </div>
        <p className="cli-tool-desc">{tool.description}</p>
      </Card>
    </Link>
  );
}
