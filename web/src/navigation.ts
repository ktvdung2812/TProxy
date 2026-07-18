export type NavRoute = {
  id: string;
  path: string;
  label: string;
  icon: string;
  title: string;
  description: string;
};

export type NavSection = {
  id: string;
  label: string;
  routes: NavRoute[];
};

export const NAV_SECTIONS: NavSection[] = [
  {
    id: "dashboard",
    label: "Dashboard",
    routes: [
      {
        id: "overview",
        path: "/",
        label: "Overview",
        icon: "space_dashboard",
        title: "Routing overview",
        description: "Stable model IDs across every upstream.",
      },
    ],
  },
  {
    id: "routing",
    label: "Routing",
    routes: [
      {
        id: "models",
        path: "/models",
        label: "Provider Priority Manager",
        icon: "route",
        title: "Provider Priority Manager",
        description: "Map providers to stable model IDs and control fallback order.",
      },
      {
        id: "combos",
        path: "/combos",
        label: "Combos",
        icon: "layers",
        title: "Combos",
        description: "Ordered fallback policies across virtual models.",
      },
      {
        id: "mapping",
        path: "/mapping",
        label: "Mapping",
        icon: "swap_horiz",
        title: "Protocol mapping",
        description: "Transparent Claude tier routing — rewrite placeholders server-side without changing client model names.",
      },
    ],
  },
  {
    id: "infrastructure",
    label: "Infrastructure",
    routes: [
      {
        id: "providers",
        path: "/providers",
        label: "Providers",
        icon: "dns",
        title: "Providers",
        description: "Manage your AI provider connections.",
      },
      {
        id: "upstreams",
        path: "/upstreams",
        label: "Upstreams",
        icon: "cloud",
        title: "Upstreams",
        description: "Configured upstream gateways and accounts.",
      },
      {
        id: "proxy-pools",
        path: "/proxy-pools",
        label: "Proxy pools",
        icon: "lan",
        title: "Proxy pools",
        description: "Encrypted egress pools bound to providers and credentials.",
      },
    ],
  },
  {
    id: "monitoring",
    label: "Monitoring",
    routes: [
      {
        id: "usage",
        path: "/usage",
        label: "Usage",
        icon: "monitoring",
        title: "Usage",
        description: "Token usage, estimated cost, and request history.",
      },
      {
        id: "quota",
        path: "/quota",
        label: "Quota Tracker",
        icon: "data_usage",
        title: "Quota Tracker",
        description: "Track and manage your API quota limits.",
      },
    ],
  },
  {
    id: "developer",
    label: "Developer",
    routes: [
      {
        id: "apis",
        path: "/apis",
        label: "APIs",
        icon: "api",
        title: "API Endpoint",
        description: "Gateway URL, client API keys, and authentication.",
      },
      {
        id: "chat",
        path: "/chat",
        label: "Chat",
        icon: "chat",
        title: "Chat",
        description: "Test models with a conversational playground.",
      },
      {
        id: "cli-tools",
        path: "/cli-tools",
        label: "CLI Tools",
        icon: "terminal",
        title: "CLI Tools",
        description: "Connect coding CLIs and IDE extensions to tproxy.",
      },
    ],
  },
  {
    id: "system",
    label: "System",
    routes: [
      {
        id: "observability",
        path: "/logs",
        label: "Logs & audit",
        icon: "monitoring",
        title: "Logs & audit",
        description: "Recent requests and admin changes.",
      },
    ],
  },
];

export const ALL_ROUTES = NAV_SECTIONS.flatMap((section) => section.routes);

/** @deprecated Use NAV_SECTIONS */
export const PRIMARY_ROUTES = NAV_SECTIONS.filter((section) => section.id !== "system").flatMap((section) => section.routes);

/** @deprecated Use NAV_SECTIONS */
export const SYSTEM_ROUTES = NAV_SECTIONS.find((section) => section.id === "system")?.routes ?? [];

export function matchRoute(pathname: string): NavRoute {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  if (normalized.startsWith("/providers")) {
    return ALL_ROUTES.find((route) => route.id === "providers") ?? ALL_ROUTES[0];
  }
  if (normalized.startsWith("/cli-tools")) {
    return ALL_ROUTES.find((route) => route.id === "cli-tools") ?? ALL_ROUTES[0];
  }
  return ALL_ROUTES.find((route) => route.path === normalized) ?? ALL_ROUTES[0];
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
