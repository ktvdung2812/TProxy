export type ApiKeyLimits = {
  requests_per_minute?: number;
  concurrent_streams?: number;
  max_input_bytes?: number;
  max_output_tokens?: number;
  media_jobs?: number;
  budget_usd_per_day?: number;
};

export type ApiKeyPolicy = {
  limits?: ApiKeyLimits;
  endpoints?: string[];
  team?: string;
  disable_model_mapping?: boolean;
};

export type ApiKeyRecord = {
  id: string;
  name: string;
  models: string[];
  enabled: boolean;
  policy?: ApiKeyPolicy;
};

export type ApiKeyUsage = {
  id: string;
  name: string;
  enabled: boolean;
  requests_today: number;
  cost_usd_today: number;
  budget_usd_per_day?: number;
  requests_per_minute?: number;
};

export type ApiKeyFormData = {
  id: string;
  name: string;
  models: string;
  enabled: boolean;
  team: string;
  endpoints: string;
  rpm: number;
  streams: number;
  max_input_bytes: number;
  max_output_tokens: number;
  media_jobs: number;
  budget_usd_per_day: number;
  disable_model_mapping: boolean;
};

export type ApiModelOption = {
  value: string;
  label: string;
  group: "models" | "combos";
};

export type ProxyEndpoint = {
  path: string;
  methods: string[];
  description: string;
  capability?: string;
};
