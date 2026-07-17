import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { getProviderTypeInfo } from "../providers/catalog";
import type { UsageActiveRequest } from "./api";

type ProviderItem = {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
};

type Props = {
  providers: ProviderItem[];
  activeRequests?: UsageActiveRequest[];
  lastProvider?: string;
  errorProvider?: string;
  aside?: ReactNode;
};

const FE_ACTIVE_TIMEOUT_MS = 60_000;
const FE_ACTIVE_TICK_MS = 1_000;

function getProviderColor(type: string): string {
  return getProviderTypeInfo(type).color || "#64748b";
}

function ProviderNode({ data }: NodeProps) {
  const { label, color, textIcon, active } = data as {
    label: string;
    color: string;
    textIcon: string;
    active: boolean;
  };

  return (
    <div
      className="usage-flow-provider-node"
      style={{
        borderColor: active ? color : "var(--color-border)",
        boxShadow: active ? `0 0 16px ${color}40` : "none",
      }}
    >
      <Handle type="target" position={Position.Top} id="top" className="usage-flow-handle" />
      <Handle type="target" position={Position.Bottom} id="bottom" className="usage-flow-handle" />
      <Handle type="target" position={Position.Left} id="left" className="usage-flow-handle" />
      <Handle type="target" position={Position.Right} id="right" className="usage-flow-handle" />

      <div className="usage-flow-provider-icon" style={{ backgroundColor: `${color}15` }}>
        <span style={{ color }}>{textIcon}</span>
      </div>
      <span className="usage-flow-provider-label" style={{ color: active ? color : "var(--color-text-main)" }}>
        {label}
      </span>
      {active ? (
        <span className="usage-flow-active-dot">
          <span className="usage-flow-active-ping" style={{ backgroundColor: color }} />
          <span className="usage-flow-active-core" style={{ backgroundColor: color }} />
        </span>
      ) : null}
    </div>
  );
}

function RouterNode({ data }: NodeProps) {
  const activeCount = Number((data as { activeCount?: number }).activeCount || 0);
  return (
    <div className="usage-flow-router-node">
      <Handle type="source" position={Position.Top} id="top" className="usage-flow-handle" />
      <Handle type="source" position={Position.Bottom} id="bottom" className="usage-flow-handle" />
      <Handle type="source" position={Position.Left} id="left" className="usage-flow-handle" />
      <Handle type="source" position={Position.Right} id="right" className="usage-flow-handle" />
      <span className="material-symbols-outlined">device_hub</span>
      <span>tproxy</span>
      {activeCount > 0 ? <span className="usage-flow-router-count">{activeCount}</span> : null}
    </div>
  );
}

const nodeTypes = { provider: ProviderNode, router: RouterNode };

function buildLayout(
  providers: ProviderItem[],
  activeSet: Set<string>,
  lastSet: Set<string>,
  errorSet: Set<string>,
): { nodes: Node[]; edges: Edge[] } {
  const nodeW = 180;
  const nodeH = 30;
  const routerW = 120;
  const routerH = 44;
  const nodeGap = 24;
  const count = providers.length;
  const minRx = ((nodeW + nodeGap) * count) / (2 * Math.PI);
  const rx = Math.max(320, minRx);
  const ry = Math.max(200, rx * 0.55);

  if (count === 0) {
    return {
      nodes: [{
        id: "router",
        type: "router",
        position: { x: 0, y: 0 },
        data: { activeCount: 0 },
        draggable: false,
      }],
      edges: [],
    };
  }

  const nodes: Node[] = [];
  const edges: Edge[] = [];

  nodes.push({
    id: "router",
    type: "router",
    position: { x: -routerW / 2, y: -routerH / 2 },
    data: { activeCount: activeSet.size },
    draggable: false,
  });

  const edgeStyle = (active: boolean, last: boolean, error: boolean) => {
    if (error) return { stroke: "#ef4444", strokeWidth: 2.5, opacity: 0.9 };
    if (active) return { stroke: "#22c55e", strokeWidth: 2.5, opacity: 0.9 };
    if (last) return { stroke: "#f59e0b", strokeWidth: 2, opacity: 0.7 };
    return { stroke: "var(--color-border)", strokeWidth: 1, opacity: 0.3 };
  };

  providers.forEach((provider, index) => {
    const providerKey = provider.id.toLowerCase();
    const color = getProviderColor(provider.type);
    const active = activeSet.has(providerKey) || activeSet.has(provider.name.toLowerCase());
    const last = !active && (lastSet.has(providerKey) || lastSet.has(provider.name.toLowerCase()));
    const error = !active && (errorSet.has(providerKey) || errorSet.has(provider.name.toLowerCase()));
    const nodeId = `provider-${provider.id}`;
    const angle = -Math.PI / 2 + (2 * Math.PI * index) / count;
    const cx = rx * Math.cos(angle);
    const cy = ry * Math.sin(angle);

    let sourceHandle = "right";
    let targetHandle = "left";
    if (Math.abs(angle + Math.PI / 2) < Math.PI / 4 || Math.abs(angle - (3 * Math.PI) / 2) < Math.PI / 4) {
      sourceHandle = "top";
      targetHandle = "bottom";
    } else if (Math.abs(angle - Math.PI / 2) < Math.PI / 4) {
      sourceHandle = "bottom";
      targetHandle = "top";
    } else if (cx <= 0) {
      sourceHandle = "left";
      targetHandle = "right";
    }

    nodes.push({
      id: nodeId,
      type: "provider",
      position: { x: cx - nodeW / 2, y: cy - nodeH / 2 },
      data: {
        label: provider.name || provider.id,
        color,
        textIcon: getProviderTypeInfo(provider.type).textIcon || provider.type.slice(0, 2).toUpperCase(),
        active,
      },
      draggable: false,
    });

    edges.push({
      id: `e-${nodeId}`,
      source: "router",
      sourceHandle,
      target: nodeId,
      targetHandle,
      animated: active,
      style: edgeStyle(active, last, error),
    });
  });

  return { nodes, edges };
}

export function ProviderTopology({
  providers,
  activeRequests = [],
  lastProvider = "",
  errorProvider = "",
  aside,
}: Props) {
  const activeKey = useMemo(
    () => activeRequests.map((item) => item.provider?.toLowerCase()).filter(Boolean).sort().join(","),
    [activeRequests],
  );
  const lastKey = lastProvider.toLowerCase();
  const errorKey = errorProvider.toLowerCase();

  const rawActiveSet = useMemo(() => new Set(activeKey ? activeKey.split(",") : []), [activeKey]);
  const lastSet = useMemo(() => new Set(lastKey ? [lastKey] : []), [lastKey]);
  const errorSet = useMemo(() => new Set(errorKey ? [errorKey] : []), [errorKey]);

  const firstSeenRef = useRef<Record<string, number>>({});
  const [tick, setTick] = useState(0);

  useEffect(() => {
    const seen = firstSeenRef.current;
    const now = Date.now();
    for (const provider of rawActiveSet) {
      if (!seen[provider]) seen[provider] = now;
    }
    for (const provider of Object.keys(seen)) {
      if (!rawActiveSet.has(provider)) delete seen[provider];
    }
  }, [rawActiveSet]);

  useEffect(() => {
    if (rawActiveSet.size === 0) return undefined;
    const id = window.setInterval(() => setTick((value) => value + 1), FE_ACTIVE_TICK_MS);
    return () => window.clearInterval(id);
  }, [rawActiveSet]);

  const activeSet = useMemo(() => {
    const now = Date.now();
    const filtered = new Set<string>();
    for (const provider of rawActiveSet) {
      const ts = firstSeenRef.current[provider];
      if (!ts || now - ts < FE_ACTIVE_TIMEOUT_MS) filtered.add(provider);
    }
    return filtered;
  }, [rawActiveSet, tick]);

  const { nodes, edges } = useMemo(
    () => buildLayout(providers, activeSet, lastSet, errorSet),
    [providers, activeSet, lastSet, errorSet],
  );

  const providersKey = useMemo(
    () => providers.map((provider) => provider.id).sort().join(","),
    [providers],
  );

  const rfInstance = useRef<{ fitView: (options?: { padding?: number; duration?: number }) => void } | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const fitOpts = { padding: 0.2, duration: 200 };

  const onInit = useCallback((instance: { fitView: (options?: { padding?: number; duration?: number }) => void }) => {
    rfInstance.current = instance;
    window.setTimeout(() => instance.fitView(fitOpts), 50);
  }, []);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return undefined;
    const observer = new ResizeObserver(() => {
      rfInstance.current?.fitView(fitOpts);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!rfInstance.current) return undefined;
    const id = window.setTimeout(() => rfInstance.current?.fitView(fitOpts), 50);
    return () => window.clearTimeout(id);
  }, [nodes.length]);

  return (
    <div className="usage-topology-shell">
      <div className="usage-topology-head">
        <span className="material-symbols-outlined">hub</span>
        <div>
          <strong>Provider activity</strong>
          <p>Live routing through configured upstream providers.</p>
        </div>
      </div>
      <div className={aside ? "usage-topology-body" : undefined}>
        <div ref={containerRef} className="usage-flow-canvas">
          {providers.length === 0 ? (
            <div className="usage-flow-empty">No providers configured</div>
          ) : (
            <ReactFlow
              key={providersKey}
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              fitView
              fitViewOptions={fitOpts}
              minZoom={0.1}
              maxZoom={2}
              onInit={onInit}
              proOptions={{ hideAttribution: true }}
              panOnDrag
              zoomOnScroll
              zoomOnPinch
              zoomOnDoubleClick
              preventScrolling={false}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable={false}
            >
              <Controls showInteractive={false} className="react-flow-controls-custom" />
            </ReactFlow>
          )}
        </div>
        {aside ? <div className="usage-topology-aside">{aside}</div> : null}
      </div>
    </div>
  );
}
