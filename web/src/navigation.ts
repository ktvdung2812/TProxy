export type NavRoute = {
  id: string;
  path: string;
  i18nKey: string;
  icon: string;
};

export type NavSection = {
  id: string;
  i18nKey: string;
  routes: NavRoute[];
};

export const NAV_SECTIONS: NavSection[] = [
  {
    id: "dashboard",
    i18nKey: "nav.dashboard",
    routes: [
      {
        id: "overview",
        path: "/",
        i18nKey: "nav.overview",
        icon: "space_dashboard",
      },
    ],
  },
  {
    id: "routing",
    i18nKey: "nav.routing",
    routes: [
      {
        id: "models",
        path: "/models",
        i18nKey: "nav.ppm",
        icon: "route",
      },
      {
        id: "combos",
        path: "/combos",
        i18nKey: "nav.combos",
        icon: "layers",
      },
      {
        id: "mapping",
        path: "/mapping",
        i18nKey: "nav.mapping",
        icon: "swap_horiz",
      },
    ],
  },
  {
    id: "infrastructure",
    i18nKey: "nav.infrastructure",
    routes: [
      {
        id: "providers",
        path: "/providers",
        i18nKey: "nav.providers",
        icon: "dns",
      },
      {
        id: "upstreams",
        path: "/upstreams",
        i18nKey: "nav.healthOverview",
        icon: "cloud",
      },
      {
        id: "proxy-pools",
        path: "/proxy-pools",
        i18nKey: "nav.proxyPools",
        icon: "lan",
      },
    ],
  },
  {
    id: "monitoring",
    i18nKey: "nav.monitoring",
    routes: [
      {
        id: "usage",
        path: "/usage",
        i18nKey: "nav.usage",
        icon: "monitoring",
      },
      {
        id: "token-saver",
        path: "/token-saver",
        i18nKey: "nav.tokenSaver",
        icon: "compress",
      },
      {
        id: "quota",
        path: "/quota",
        i18nKey: "nav.quotaTracker",
        icon: "data_usage",
      },
      {
        id: "free-tiers",
        path: "/free-tiers",
        i18nKey: "nav.freeTiers",
        icon: "savings",
      },
    ],
  },
  {
    id: "developer",
    i18nKey: "nav.developer",
    routes: [
      {
        id: "apis",
        path: "/apis",
        i18nKey: "nav.apis",
        icon: "api",
      },
      {
        id: "chat",
        path: "/chat",
        i18nKey: "nav.chat",
        icon: "chat",
      },
      {
        id: "cli-tools",
        path: "/cli-tools",
        i18nKey: "nav.cliTools",
        icon: "terminal",
      },
      {
        id: "skills",
        path: "/skills",
        i18nKey: "nav.skills",
        icon: "auto_awesome",
      },
    ],
  },
  {
    id: "system",
    i18nKey: "nav.system",
    routes: [
      {
        id: "observability",
        path: "/logs",
        i18nKey: "nav.logs",
        icon: "receipt_long",
      },
      {
        id: "settings",
        path: "/settings",
        i18nKey: "nav.settings",
        icon: "settings",
      },
    ],
  },
];

export const ALL_ROUTES = NAV_SECTIONS.flatMap((section) => section.routes);

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
