import { Link, Navigate, useParams } from "react-router-dom";
import { getAnyTool } from "../../cli-tools/constants";
import { buildModelOptions } from "../../lib/modelOptions";
import { ToolGuideCard } from "./ToolGuideCard";

type SnapshotSlice = {
  models: { ID: string; DisplayName?: string; Enabled?: boolean }[] | null;
  combos: { id: string; display_name?: string; enabled?: boolean }[] | null;
  api_keys: { id: string; name: string; enabled?: boolean }[] | null;
};

type Props = {
  snapshot: SnapshotSlice;
  secret: string;
};

export function CLIToolDetailView({ snapshot, secret }: Props) {
  const { toolId } = useParams<{ toolId: string }>();
  const tool = toolId ? getAnyTool(toolId) : undefined;

  if (!toolId) {
    return <Navigate to="/cli-tools" replace />;
  }

  if (!tool) {
    return (
      <section className="section">
        <Link to="/cli-tools" className="cli-tool-back">
          <span className="material-symbols-outlined">arrow_back</span>
          Back to CLI Tools
        </Link>
        <p className="cli-tool-hint">Tool not found or disabled.</p>
      </section>
    );
  }

  const models = buildModelOptions(snapshot.models ?? [], snapshot.combos ?? [], tool.defaultModels);
  const apiKeys = (snapshot.api_keys ?? []).map((key) => ({
    id: key.id,
    name: key.name || key.id,
    enabled: key.enabled,
  }));
  const isMitm = tool.configType === "mitm";

  return (
    <section className="section cli-tools-detail">
      <Link to="/cli-tools" className="cli-tool-back">
        <span className="material-symbols-outlined">arrow_back</span>
        Back to CLI Tools
      </Link>
      <div className="section-head">
        <div>
          <p className="eyebrow">{isMitm ? "MITM setup" : "CLI setup"}</p>
          <h2>{tool.name}</h2>
          <p>{tool.description}</p>
        </div>
      </div>
      {isMitm && tool.mitmDomain ? (
        <div className="cli-tool-mitm-meta">
          <span className="cli-tool-mitm-badge">MITM</span>
          <code>{tool.mitmDomain}</code>
        </div>
      ) : null}
      <ToolGuideCard tool={tool} models={models} apiKeys={apiKeys} secret={secret} />
    </section>
  );
}
