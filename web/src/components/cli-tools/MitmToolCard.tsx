import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Card } from "../ui";
import type { CLITool } from "../../cli-tools/constants";

type Props = {
  toolId: string;
  tool: CLITool;
};

export function MitmToolCard({ toolId, tool }: Props) {
  const { t } = useTranslation();

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
              <span className="cli-tool-mitm-badge">{t("cliTools.mitmBadge")}</span>
            </div>
          </div>
          <span className="material-symbols-outlined cli-tool-chevron">chevron_right</span>
        </div>
        <p className="cli-tool-desc">{tool.description}</p>
      </Card>
    </Link>
  );
}
