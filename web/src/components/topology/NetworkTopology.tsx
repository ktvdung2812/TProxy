import { useTranslation } from "react-i18next";
import "./topology-localization.css";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  BaseEdge,
  Controls,
  Handle,
  Position,
  ReactFlow,
  getBezierPath,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { UsageActiveRequest } from "../usage/api";
import { ProviderLogo } from "../providers/ProviderLogo";
import { fetchTopologyClients, type TopologyClient } from "./api";
import { ClientDetailModal } from "./ClientDetailModal";
import {
  FLOW_SOURCE_BOTTOM,
  FLOW_TARGET_TOP,
  buildTproxyTopologyFlow,
  formatNumber,
  type CredentialItem,
  type FlowEdgeData,
  type ProviderItem,
} from "./utils";
import { maskAccountText } from "../../lib/accountMask";
import { useAccountMask } from "../../hooks/useAccountMask";

type Props = {
  secret: string;
  providers: ProviderItem[];
  credentialsByProvider: Record<string, CredentialItem[]>;
  activeRequests?: UsageActiveRequest[];
};

function FlowEdgeComponent({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<Edge<FlowEdgeData>>) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });
  const color = data?.color || "var(--color-border)";
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        style={{
          stroke: color,
          strokeWidth: data?.animated ? 2.5 : 1.5,
          opacity: data?.animated ? 0.95 : 0.45,
        }}
      />
      {data?.label ? (
        <text x={labelX} y={labelY - 6} className="topology-edge-label">
          {data.label}
        </text>
      ) : null}
    </>
  );
}

function GatewayNode({ data }: NodeProps) {
  const { t } = useTranslation();
  const payload = data as {
    label: string;
    totalRequests: number;
    todayRequests: number;
    activeProviders: number;
    activeCount: number;
  };
  return (
    <div className="topology-node topology-node-gateway">
      <Handle id={FLOW_TARGET_TOP} type="target" position={Position.Top} className="topology-handle" />
      <Handle id={FLOW_SOURCE_BOTTOM} type="source" position={Position.Bottom} className="topology-handle" />
      <span className="material-symbols-outlined">device_hub</span>
      <div>
        <strong>{payload.label}</strong>
        <p>{t("featureB.topology.gatewaySummary", { count: payload.activeProviders, requests: formatNumber(payload.todayRequests), providers: formatNumber(payload.activeProviders) })}</p>
      </div>
      {payload.activeCount > 0 ? <span className="topology-badge">{payload.activeCount}</span> : null}
    </div>
  );
}

function ProviderNode({ data }: NodeProps) {
  const { t } = useTranslation();
  const payload = data as {
    label: string;
    providerId: string;
    providerType: string;
    color: string;
    totalRequests: number;
    credentialCount: number;
    active: boolean;
  };
  return (
    <div
      className={`topology-node topology-node-provider ${payload.active ? "active" : ""}`}
      style={{ borderColor: payload.active ? payload.color : "var(--color-border)" }}
    >
      <Handle id={FLOW_TARGET_TOP} type="target" position={Position.Top} className="topology-handle" />
      <Handle id={FLOW_SOURCE_BOTTOM} type="source" position={Position.Bottom} className="topology-handle" />
      <ProviderLogo
        className="topology-node-icon"
        providerId={payload.providerId}
        providerType={payload.providerType}
        style={{ color: payload.color }}
      />
      <div>
        <strong>{payload.label}</strong>
        <p>{t("featureB.topology.providerSummary", { count: payload.credentialCount, credentials: formatNumber(payload.credentialCount), requests: t("featureB.topology.requestCount", { count: payload.totalRequests, value: formatNumber(payload.totalRequests) }) })}</p>
      </div>
    </div>
  );
}

function CredentialNode({ data }: NodeProps) {
  const { t } = useTranslation();
  const { masked } = useAccountMask();
  const payload = data as {
    label: string;
    color: string;
    totalRequests: number;
    enabled: boolean;
    active: boolean;
  };
  return (
    <div className={`topology-node topology-node-credential ${payload.active ? "active" : ""} ${payload.enabled ? "" : "disabled"}`}>
      <Handle id={FLOW_TARGET_TOP} type="target" position={Position.Top} className="topology-handle" />
      <span className="topology-dot" style={{ backgroundColor: payload.enabled ? payload.color : "#477453" }} />
      <div>
        <strong>{maskAccountText(payload.label, masked)}</strong>
        <p>{t("featureB.topology.requestCount", { count: payload.totalRequests, value: formatNumber(payload.totalRequests) })}</p>
      </div>
    </div>
  );
}

function ClientNode({ data }: NodeProps) {
  const { t } = useTranslation();
  const payload = data as {
    label: string;
    totalRequests: number;
    todayRequests: number;
    lastSeen: string;
    stale: boolean;
    active: boolean;
  };
  return (
    <div className={`topology-node topology-node-client ${payload.active ? "active" : ""} ${payload.stale ? "stale" : ""}`}>
      <Handle id={FLOW_SOURCE_BOTTOM} type="source" position={Position.Bottom} className="topology-handle" />
      <span className="material-symbols-outlined">laptop_mac</span>
      <div>
        <strong>{payload.label}</strong>
        <p>{t("featureB.topology.clientSummary", { requests: formatNumber(payload.todayRequests), lastSeen: payload.lastSeen || t("featureB.topology.noActivity") })}</p>
      </div>
    </div>
  );
}

const nodeTypes = {
  gateway: GatewayNode,
  provider: ProviderNode,
  credential: CredentialNode,
  client: ClientNode,
};

const edgeTypes = {
  flow: FlowEdgeComponent,
};

export function NetworkTopology({
  secret,
  providers,
  credentialsByProvider,
  activeRequests = [],
}: Props) {
  const { t, i18n } = useTranslation();
  const [clients, setClients] = useState<TopologyClient[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedClient, setSelectedClient] = useState<{ id: string; label: string } | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const rfInstance = useRef<{ fitView: (options?: { padding?: number; duration?: number }) => void } | null>(null);

  const loadClients = useCallback(() => {
    fetchTopologyClients(secret)
      .then((data) => { setClients(data); setError(""); })
      .catch((cause) => setError(cause instanceof Error ? cause.message : t("featureB.topology.loadClients")))
      .finally(() => setLoading(false));
  }, [secret, t]);

  useEffect(() => {
    loadClients();
    const timer = window.setInterval(loadClients, 30_000);
    return () => window.clearInterval(timer);
  }, [loadClients]);

  const { nodes, edges } = useMemo(
    () => buildTproxyTopologyFlow({ clients, providers, credentialsByProvider, activeRequests }),
    [clients, providers, credentialsByProvider, activeRequests, i18n.resolvedLanguage],
  );

  const onInit = useCallback((instance: { fitView: (options?: { padding?: number; duration?: number }) => void }) => {
    rfInstance.current = instance;
    window.setTimeout(() => instance.fitView({ padding: 0.15, duration: 200 }), 50);
  }, []);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return undefined;
    const observer = new ResizeObserver(() => rfInstance.current?.fitView({ padding: 0.15, duration: 200 }));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const onNodeClick = useCallback((_: unknown, node: Node) => {
    if (node.type !== "client") return;
    const data = node.data as { clientKeyId: string; label: string };
    setSelectedClient({ id: data.clientKeyId, label: data.label });
  }, []);

  return (
    <div className="usage-topology-shell network-topology-shell">
      <div className="usage-topology-head">
        <span className="material-symbols-outlined">account_tree</span>
        <div>
          <strong>{t("featureB.topology.title")}</strong>
          <p>{t("featureB.topology.description")}</p>
        </div>
      </div>

      <div ref={containerRef} className="usage-flow-canvas network-topology-canvas">
        {loading ? (
          <div className="usage-flow-empty" role="status" aria-label={t("common.loading")}>
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
          </div>
        ) : error ? (
          <div className="usage-flow-empty">{error}</div>
        ) : nodes.length <= 1 ? (
          <div className="usage-flow-empty">{t("topology.noTraffic")}</div>
        ) : (
          <ReactFlow
            ariaLabelConfig={{
            "controls.ariaLabel": t("featureB.flow.controls"),
            "controls.zoomIn.ariaLabel": t("featureB.flow.zoomIn"),
            "controls.zoomOut.ariaLabel": t("featureB.flow.zoomOut"),
            "controls.fitView.ariaLabel": t("featureB.flow.fitView"),
            "node.a11yDescription.default": t("featureB.flow.node"),
            "node.a11yDescription.keyboardDisabled": t("featureB.flow.node"),
            "edge.a11yDescription.default": t("featureB.flow.edge"),
            "handle.ariaLabel": t("featureB.flow.handle"),
          }}
          nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            onInit={onInit}
            onNodeClick={onNodeClick}
            fitView
            fitViewOptions={{ padding: 0.15 }}
            minZoom={0.1}
            maxZoom={1.5}
            proOptions={{ hideAttribution: true }}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={false}
            panOnDrag
            zoomOnScroll
          >
            <Controls showInteractive={false} className="react-flow-controls-custom" />
          </ReactFlow>
        )}
      </div>

      {selectedClient ? (
        <ClientDetailModal
          secret={secret}
          clientKeyId={selectedClient.id}
          clientLabel={selectedClient.label}
          onClose={() => setSelectedClient(null)}
        />
      ) : null}
    </div>
  );
}
