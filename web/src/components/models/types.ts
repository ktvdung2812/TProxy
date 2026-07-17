export type ModelRecord = {
  ID: string;
  DisplayName: string;
  Aliases: string[];
  Enabled: boolean;
  RewriteResponseModel?: boolean;
  Capabilities: string[];
};

export type RouteRecord = {
  ID: string;
  ProviderID: string;
  UpstreamModel: string;
  Priority: number;
  Weight?: number;
  Enabled: boolean;
};

export type ProviderOption = {
  id: string;
  label: string;
  type: string;
};

export type RouteFormData = {
  id: string;
  provider: string;
  upstream_model: string;
  priority: number;
  weight: number;
  enabled: boolean;
};

export type ModelFormData = {
  id: string;
  display_name: string;
  aliases: string;
  enabled: boolean;
  rewrite_response_model: boolean;
  capabilities: string[];
  routes: RouteFormData[];
};
