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
import { fetchTopologyClients, type TopologyClient } from "./api";
import { ClientDetailModal } from "./ClientDetailModal";
import {
  FLOW_SOURCE_RIGHT,
  FLOW_TARGET_LEFT,
  buildTproxyTopologyFlow,
  formatNumber,
  type CredentialItem,
  type FlowEdgeData,
  type ProviderItem,
} from "./utils";

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
  const payload = data as {
    label: string;
    totalRequests: number;
    todayRequests: number;
    activeProviders: number;
    activeCount: number;
  };
  return (
    <div className="topology-node topology-node-gateway">
      <Handle id={FLOW_TARGET_LEFT} type="target" position={Position.Left} className="topology-handle" />
      <Handle id={FLOW_SOURCE_RIGHT} type="source" position={Position.Right} className="topology-handle" />
      <span className="material-symbols-outlined">device_hub</span>
      <div>
        <strong>{payload.label}</strong>
        <p>{formatNumber(payload.todayRequests)} today · {payload.activeProviders} providers</p>
      </div>
      {payload.activeCount > 0 ? <span className="topology-badge">{payload.activeCount}</span> : null}
    </div>
  );
}

function ProviderNode({ data }: NodeProps) {
  const payload = data as {
    label: string;
    color: string;
    textIcon: string;
    totalRequests: number;
    credentialCount: number;
    active: boolean;
  };
  return (
    <div
      className={`topology-node topology-node-provider ${payload.active ? "active" : ""}`}
      style={{ borderColor: payload.active ? payload.color : "var(--color-border)" }}
    >
      <Handle id={FLOW_TARGET_LEFT} type="target" position={Position.Left} className="topology-handle" />
      <Handle id={FLOW_SOURCE_RIGHT} type="source" position={Position.Right} className="topology-handle" />
      <span className="topology-node-icon" style={{ backgroundColor: `${payload.color}18`, color: payload.color }}>
        {payload.textIcon}
      </span>
      <div>
        <strong>{payload.label}</strong>
        <p>{payload.credentialCount} credentials · {formatNumber(payload.totalRequests)} req</p>
      </div>
    </div>
  );
}

function CredentialNode({ data }: NodeProps) {
  const payload = data as {
    label: string;
    color: string;
    totalRequests: number;
    enabled: boolean;
    active: boolean;
  };
  return (
    <div className={`topology-node topology-node-credential ${payload.active ? "active" : ""} ${payload.enabled ? "" : "disabled"}`}>
      <Handle id={FLOW_TARGET_LEFT} type="target" position={Position.Left} className="topology-handle" />
      <span className="topology-dot" style={{ backgroundColor: payload.enabled ? payload.color : "#64748b" }} />
      <div>
        <strong>{payload.label}</strong>
        <p>{formatNumber(payload.totalRequests)} requests</p>
      </div>
    </div>
  );
}

function ClientNode({ data }: NodeProps) {
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
      <Handle id={FLOW_SOURCE_RIGHT} type="source" position={Position.Right} className="topology-handle" />
      <span className="material-symbols-outlined">laptop_mac</span>
      <div>
        <strong>{payload.label}</strong>
        <p>{formatNumber(payload.todayRequests)}/day · {payload.lastSeen || "no activity"}</p>
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
  const [clients, setClients] = useState<TopologyClient[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedClient, setSelectedClient] = useState<{ id: string; label: string } | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const rfInstance = useRef<{ fitView: (options?: { padding?: number; duration?: number }) => void } | null>(null);

  const loadClients = useCallback(() => {
    fetchTopologyClients(secret)
      .then((data) => setClients(data))
      .catch((cause) => setError(cause instanceof Error ? cause.message : "Failed to load topology clients"))
      .finally(() => setLoading(false));
  }, [secret]);

  useEffect(() => {
    loadClients();
    const timer = window.setInterval(loadClients, 30_000);
    return () => window.clearInterval(timer);
  }, [loadClients]);

  const { nodes, edges } = useMemo(
    () => buildTproxyTopologyFlow({ clients, providers, credentialsByProvider, activeRequests }),
    [clients, providers, credentialsByProvider, activeRequests],
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
          <strong>Request flow topology</strong>
          <p>Clients → tproxy → providers → credentials, based on usage history.</p>
        </div>
      </div>

      <div ref={containerRef} className="usage-flow-canvas network-topology-canvas">
        {loading ? (
          <div className="usage-flow-empty">
            <span className="material-symbols-outlined animate-spin">progress_activity</span>
          </div>
        ) : error ? (
          <div className="usage-flow-empty">{error}</div>
        ) : nodes.length <= 1 ? (
          <div className="usage-flow-empty">No client traffic recorded yet.</div>
        ) : (
          <ReactFlow
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
