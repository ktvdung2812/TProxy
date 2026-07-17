import type { Edge, Node } from "@xyflow/react";
import { getProviderTypeInfo } from "../providers/catalog";
import type { UsageActiveRequest } from "../usage/api";
import type { TopologyClient } from "./api";

export const FLOW_TARGET_LEFT = "target-left";
export const FLOW_SOURCE_RIGHT = "source-right";

export type ProviderItem = {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
};

export type CredentialItem = {
  id: string;
  label?: string;
  email?: string;
  enabled?: boolean;
};

export type GatewayNodeData = {
  label: string;
  totalRequests: number;
  todayRequests: number;
  activeProviders: number;
  activeCount: number;
};

export type ProviderNodeData = {
  label: string;
  providerId: string;
  color: string;
  textIcon: string;
  todayRequests: number;
  totalRequests: number;
  credentialCount: number;
  active: boolean;
};

export type CredentialNodeData = {
  label: string;
  credentialId: string;
  providerId: string;
  color: string;
  totalRequests: number;
  enabled: boolean;
  active: boolean;
};

export type ClientNodeData = {
  label: string;
  clientKeyId: string;
  totalRequests: number;
  todayRequests: number;
  lastSeen: string;
  providers: string[];
  stale: boolean;
  active: boolean;
};

export type FlowEdgeData = {
  color: string;
  label?: string;
  animated: boolean;
};

export type FlowNode = Node<
  GatewayNodeData | ProviderNodeData | CredentialNodeData | ClientNodeData
>;
export type FlowEdge = Edge<FlowEdgeData>;

const CLIENT_IDLE_MS = 90_000;
const CLIENT_REMOVE_MS = 300_000;

export function formatNumber(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

export function timeAgo(value: string, now = Date.now()): string {
  const then = Date.parse(value);
  if (Number.isNaN(then)) return "";
  const diff = now - then;
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function providerColor(type: string): string {
  return getProviderTypeInfo(type).color || "#64748b";
}

function providerIcon(type: string): string {
  return getProviderTypeInfo(type).textIcon || type.slice(0, 2).toUpperCase();
}

function activeProviderSet(activeRequests: UsageActiveRequest[]): Set<string> {
  const set = new Set<string>();
  for (const item of activeRequests) {
    const key = item.provider?.toLowerCase();
    if (key) set.add(key);
  }
  return set;
}

export function buildTproxyTopologyFlow(input: {
  clients: TopologyClient[];
  providers: ProviderItem[];
  credentialsByProvider: Record<string, CredentialItem[]>;
  activeRequests?: UsageActiveRequest[];
  now?: number;
}): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const now = input.now ?? Date.now();
  const activeProviders = activeProviderSet(input.activeRequests || []);
  const activeCredentials = new Set(
    (input.activeRequests || []).flatMap((item) => {
      const values: string[] = [];
      if (item.account) values.push(item.account.toLowerCase());
      if (item.credential_id) values.push(item.credential_id.toLowerCase());
      return values;
    }),
  );

  const visibleClients = input.clients.filter((client) => {
    const lastSeen = Date.parse(client.last_seen_at);
    if (Number.isNaN(lastSeen)) return true;
    return now - lastSeen < CLIENT_REMOVE_MS;
  });

  const usageByProvider = new Map<string, { today: number; total: number }>();
  const usageByCredential = new Map<string, number>();
  for (const client of visibleClients) {
    for (const usage of client.model_usage || []) {
      const providerKey = usage.provider.toLowerCase();
      const current = usageByProvider.get(providerKey) || { today: 0, total: 0 };
      current.total += usage.request_count;
      usageByProvider.set(providerKey, current);
      const credentialKey = `${providerKey}:${usage.credential_id}`;
      usageByCredential.set(credentialKey, (usageByCredential.get(credentialKey) || 0) + usage.request_count);
    }
  }

  const enabledProviders = input.providers.filter((provider) => provider.enabled);
  const nodes: FlowNode[] = [];
  const edges: FlowEdge[] = [];

  const CLIENT_W = 220;
  const CLIENT_H = 72;
  const GATEWAY_W = 140;
  const GATEWAY_H = 64;
  const PROVIDER_W = 180;
  const PROVIDER_H = 64;
  const CREDENTIAL_W = 180;
  const CREDENTIAL_H = 48;
  const GAP_Y = 16;
  const GAP_X = 120;
  const LEFT = 20;
  const gatewayX = LEFT + CLIENT_W + GAP_X;
  const providerX = gatewayX + GATEWAY_W + GAP_X;
  const credentialX = providerX + PROVIDER_W + GAP_X;

  const clientCount = visibleClients.length;
  const providerCount = Math.max(enabledProviders.length, 1);
  const contentHeight = Math.max(clientCount, providerCount) * (CLIENT_H + GAP_Y);
  const centerY = 40 + contentHeight / 2;

  let totalToday = 0;
  let totalRequests = 0;
  for (const client of visibleClients) {
    totalToday += client.today_requests;
    totalRequests += client.total_requests;
  }

  const gatewayId = "gateway";
  nodes.push({
    id: gatewayId,
    type: "gateway",
    position: { x: gatewayX, y: centerY - GATEWAY_H / 2 },
    data: {
      label: "tproxy",
      totalRequests,
      todayRequests: totalToday,
      activeProviders: enabledProviders.length,
      activeCount: input.activeRequests?.length || 0,
    },
    draggable: false,
  });

  visibleClients.forEach((client, index) => {
    const clientId = `client-${client.client_key_id}`;
    const lastSeen = Date.parse(client.last_seen_at);
    const stale = !Number.isNaN(lastSeen) && now - lastSeen >= CLIENT_IDLE_MS;
    const y = centerY - ((clientCount * (CLIENT_H + GAP_Y)) / 2) + index * (CLIENT_H + GAP_Y);
    nodes.push({
      id: clientId,
      type: "client",
      position: { x: LEFT, y },
      data: {
        label: client.client_label || client.client_key_id,
        clientKeyId: client.client_key_id,
        totalRequests: client.total_requests,
        todayRequests: client.today_requests,
        lastSeen: client.last_seen_at ? timeAgo(client.last_seen_at, now) : "",
        providers: client.providers,
        stale,
        active: !stale && client.today_requests > 0,
      },
      draggable: false,
    });
    edges.push({
      id: `e-${clientId}-${gatewayId}`,
      source: clientId,
      target: gatewayId,
      sourceHandle: FLOW_SOURCE_RIGHT,
      targetHandle: FLOW_TARGET_LEFT,
      type: "flow",
      animated: !stale && client.today_requests > 0,
      data: {
        color: stale ? "#64748b" : "#8b5cf6",
        label: client.today_requests > 0 ? `${formatNumber(client.today_requests)}/day` : undefined,
        animated: !stale && client.today_requests > 0,
      },
    });
  });

  enabledProviders.forEach((provider, index) => {
    const providerId = `provider-${provider.id}`;
    const color = providerColor(provider.type);
    const providerKey = (provider.name || provider.id).toLowerCase();
    const providerIdKey = provider.id.toLowerCase();
    const active = activeProviders.has(providerKey) || activeProviders.has(providerIdKey);
    const usage = usageByProvider.get(providerKey) || usageByProvider.get(providerIdKey) || { today: 0, total: 0 };
    const credentials = input.credentialsByProvider[provider.id] || [];
    const y = centerY - ((providerCount * (PROVIDER_H + GAP_Y)) / 2) + index * (PROVIDER_H + GAP_Y);

    nodes.push({
      id: providerId,
      type: "provider",
      position: { x: providerX, y },
      data: {
        label: provider.name || provider.id,
        providerId: provider.id,
        color,
        textIcon: providerIcon(provider.type),
        todayRequests: 0,
        totalRequests: usage.total,
        credentialCount: credentials.length,
        active,
      },
      draggable: false,
    });

      edges.push({
        id: `e-${gatewayId}-${providerId}`,
        source: gatewayId,
        target: providerId,
        sourceHandle: FLOW_SOURCE_RIGHT,
        targetHandle: FLOW_TARGET_LEFT,
        type: "flow",
        animated: active,
        data: {
          color,
          label: usage.total > 0 ? `${formatNumber(usage.total)} req` : undefined,
          animated: active,
        },
      });

    credentials.forEach((credential, credentialIndex) => {
      const credentialId = `credential-${provider.id}-${credential.id}`;
      const credentialKey = `${providerKey}:${credential.id}`;
      const credentialLabel = (credential.label || credential.email || credential.id).toLowerCase();
      const requestCount = usageByCredential.get(credentialKey) || 0;
      const credentialActive =
        active &&
        (activeCredentials.has(credentialLabel) || activeCredentials.has(credential.id.toLowerCase()));
      const credentialY = y + credentialIndex * (CREDENTIAL_H + 8) - ((credentials.length - 1) * (CREDENTIAL_H + 8)) / 2;

      nodes.push({
        id: credentialId,
        type: "credential",
        position: { x: credentialX, y: credentialY },
        data: {
          label: credential.label || credential.email || credential.id,
          credentialId: credential.id,
          providerId: provider.id,
          color,
          totalRequests: requestCount,
          enabled: credential.enabled !== false,
          active: credentialActive,
        },
        draggable: false,
      });

      edges.push({
        id: `e-${providerId}-${credentialId}`,
        source: providerId,
        target: credentialId,
        sourceHandle: FLOW_SOURCE_RIGHT,
        targetHandle: FLOW_TARGET_LEFT,
        type: "flow",
        animated: credentialActive,
        data: {
          color: credential.enabled === false ? "#64748b" : color,
          animated: credentialActive,
        },
      });
    });
  });

  const nodeIds = new Set(nodes.map((node) => node.id));
  const validEdges = edges.filter((edge) => nodeIds.has(edge.source) && nodeIds.has(edge.target));
  return { nodes, edges: validEdges };
}
