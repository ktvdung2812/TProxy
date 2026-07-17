export type NavRoute = {
  id: string;
  path: string;
  label: string;
  icon: string;
  title: string;
  description: string;
  section?: "primary" | "system";
};

export const PRIMARY_ROUTES: NavRoute[] = [
  {
    id: "overview",
    path: "/",
    label: "Overview",
    icon: "space_dashboard",
    title: "Routing overview",
    description: "Stable model IDs across every upstream.",
    section: "primary",
  },
  {
    id: "combos",
    path: "/combos",
    label: "Combos",
    icon: "layers",
    title: "Combos",
    description: "Ordered fallback policies across virtual models.",
    section: "primary",
  },
  {
    id: "models",
    path: "/models",
    label: "Virtual models",
    icon: "apps",
    title: "Virtual models",
    description: "Stable IDs presented to clients, with their routes.",
    section: "primary",
  },
  {
    id: "upstreams",
    path: "/upstreams",
    label: "Upstreams",
    icon: "cloud",
    title: "Upstreams",
    description: "Configured upstream gateways and accounts.",
    section: "primary",
  },
  {
    id: "providers",
    path: "/providers",
    label: "Providers",
    icon: "dns",
    title: "Providers",
    description: "Manage your AI provider connections.",
    section: "primary",
  },
  {
    id: "proxy-pools",
    path: "/proxy-pools",
    label: "Proxy pools",
    icon: "lan",
    title: "Proxy pools",
    description: "Encrypted egress pools bound to providers and credentials.",
    section: "primary",
  },
  {
    id: "quota",
    path: "/quota",
    label: "Quota Tracker",
    icon: "data_usage",
    title: "Quota Tracker",
    description: "Track and manage your API quota limits.",
    section: "primary",
  },
  {
    id: "usage",
    path: "/usage",
    label: "Usage",
    icon: "monitoring",
    title: "Usage",
    description: "Token usage, estimated cost, and request history.",
    section: "primary",
  },
  {
    id: "apis",
    path: "/apis",
    label: "APIs",
    icon: "api",
    title: "API Endpoint",
    description: "Gateway URL, client API keys, and authentication.",
    section: "primary",
  },
  {
    id: "chat",
    path: "/chat",
    label: "Chat",
    icon: "chat",
    title: "Chat",
    description: "Test models with a conversational playground.",
    section: "primary",
  },
  {
    id: "cli-tools",
    path: "/cli-tools",
    label: "CLI Tools",
    icon: "terminal",
    title: "CLI Tools",
    description: "Connect coding CLIs and IDE extensions to tproxy.",
    section: "primary",
  },
];

export const SYSTEM_ROUTES: NavRoute[] = [
  {
    id: "observability",
    path: "/logs",
    label: "Logs & audit",
    icon: "monitoring",
    title: "Logs & audit",
    description: "Recent requests and admin changes.",
    section: "system",
  },
];

export const ALL_ROUTES = [...PRIMARY_ROUTES, ...SYSTEM_ROUTES];

export function matchRoute(pathname: string): NavRoute {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  if (normalized.startsWith("/providers")) {
    return ALL_ROUTES.find((route) => route.id === "providers") ?? PRIMARY_ROUTES[0];
  }
  if (normalized.startsWith("/cli-tools")) {
    return ALL_ROUTES.find((route) => route.id === "cli-tools") ?? PRIMARY_ROUTES[0];
  }
  return ALL_ROUTES.find((route) => route.path === normalized) ?? PRIMARY_ROUTES[0];
}

export function isRouteActive(pathname: string, route: Pick<NavRoute, "id" | "path">): boolean {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  if (route.id === "providers") {
    return normalized === "/providers" || normalized.startsWith("/providers/");
  }
  if (route.id === "cli-tools") {
    return normalized === "/cli-tools" || normalized.startsWith("/cli-tools/");
  }
  return normalized === route.path;
}
