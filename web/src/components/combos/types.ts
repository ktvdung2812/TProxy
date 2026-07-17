export type ComboItem = {
  public_model_id: string;
  route_target_id?: string;
};

export type ComboRecord = {
  id: string;
  display_name: string;
  enabled: boolean;
  rewrite_response_model?: boolean;
  capabilities?: string[];
  limits?: Record<string, unknown>;
  policy?: Record<string, unknown>;
  items: ComboItem[];
};

export type ComboFormData = {
  id: string;
  display_name: string;
  enabled: boolean;
  rewrite_response_model: boolean;
  items: ComboItem[];
  policy?: Record<string, unknown>;
};

export type ComboModelOption = {
  id: string;
  label: string;
  enabled: boolean;
};

export type ComboRouteOption = {
  id: string;
  provider_id: string;
  upstream_model: string;
  enabled: boolean;
};

export type ComboPreset = {
  id: string;
  title: string;
  description: string;
  client: "claude-code" | "cowork" | "general";
  comboId: string;
  display_name: string;
  rewrite_response_model: boolean;
  modelIds: string[];
  policy: Record<string, unknown>;
};
