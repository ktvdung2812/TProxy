import { useEffect, useState, type CSSProperties } from "react";
import { getProviderTypeInfo } from "./catalog";

export type ModelsDevLogoKind = "provider" | "lab";

const MODELS_DEV_LOGO_BASE = "https://models.dev/logos";

/**
 * Map tdproxy's adapter types to the provider IDs used by models.dev.
 *
 * Provider instances are intentionally allowed to have arbitrary IDs (for
 * example, `openai-main`), so the adapter type is the stable source for the
 * logo. Unknown adapter types may still use their ID directly as a models.dev
 * provider ID via `modelsDevLogoUrl`/`ProviderLogo`.
 */
const MODELS_DEV_PROVIDER_BY_TYPE: Record<string, string> = {
  "openai-compatible": "openai",
  "anthropic-compatible": "anthropic",
  gemini: "google",
  vertex: "google-vertex",
  // Ollama's hosted provider is the models.dev entry that represents the
  // Ollama brand; the local adapter still uses the same logo.
  ollama: "ollama-cloud",
  codex: "openai",
  claude: "anthropic",
  kimi: "kimi-for-coding",
  xai: "xai",
  antigravity: "google",
  tavily: "tavily",
  elevenlabs: "elevenlabs",
  image: "openai",
  video: "openai",
  copilot: "github-copilot",
  "vertex-partner": "google-vertex",
};

export function modelsDevLogoUrl(id: string, kind: ModelsDevLogoKind = "provider"): string {
  const encoded = encodeURIComponent(id.trim());
  return `${MODELS_DEV_LOGO_BASE}/${kind === "lab" ? "labs/" : ""}${encoded}.svg`;
}

export function modelsDevProviderId(providerId?: string, providerType?: string): string | undefined {
  const mapped = providerType ? MODELS_DEV_PROVIDER_BY_TYPE[providerType] : undefined;
  if (mapped) return mapped;

  const direct = providerId?.trim();
  return direct || undefined;
}

type Props = {
  /** Configured provider instance ID. */
  providerId?: string;
  /** tdproxy adapter type, used for the stable models.dev mapping. */
  providerType?: string;
  /** Optional explicit models.dev lab/provider ID. */
  logoId?: string;
  logoKind?: ModelsDevLogoKind;
  className?: string;
  style?: CSSProperties;
  alt?: string;
  fallbackIcon?: string;
  fallbackText?: string;
};

/**
 * Render the models.dev SVG directly on a theme-independent white tile, with a
 * local text/icon fallback when the remote asset is unavailable.
 */
export function ProviderLogo({
  providerId,
  providerType,
  logoId,
  logoKind = "provider",
  className,
  style,
  alt,
  fallbackIcon,
  fallbackText,
}: Props) {
  const info = getProviderTypeInfo(providerType || providerId || "");
  const id = logoId || modelsDevProviderId(providerId, providerType);
  const logoUrl = id ? modelsDevLogoUrl(id, logoKind) : undefined;
  const label = alt || `${info.name} logo`;
  const [logoStatus, setLogoStatus] = useState<"loading" | "loaded" | "failed">(
    logoUrl ? "loading" : "failed",
  );

  useEffect(() => {
    setLogoStatus(logoUrl ? "loading" : "failed");
  }, [logoUrl]);

  return (
    <span
      className={className}
      style={{ ...style, backgroundColor: "#fff" }}
      data-models-dev-logo={id}
      role={alt ? "img" : undefined}
      aria-label={alt}
      aria-hidden={alt ? undefined : true}
    >
      <span className="models-dev-logo-stack" aria-hidden="true">
        {logoStatus !== "loaded" ? (
          <span className="models-dev-logo-fallback">
            {fallbackText ? (
              fallbackText
            ) : fallbackIcon ? (
              <span className="material-symbols-outlined">{fallbackIcon}</span>
            ) : (
              info.textIcon
            )}
          </span>
        ) : null}
        {logoUrl && logoStatus !== "failed" ? (
          <img
            className="models-dev-logo-image"
            src={logoUrl}
            alt=""
            title={label}
            onLoad={() => setLogoStatus("loaded")}
            onError={() => setLogoStatus("failed")}
          />
        ) : null}
      </span>
    </span>
  );
}

export function LabLogo(props: Omit<Props, "logoKind">) {
  return <ProviderLogo {...props} logoKind="lab" logoId={props.logoId || props.providerId} />;
}
