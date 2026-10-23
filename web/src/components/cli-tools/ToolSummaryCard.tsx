import type { KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Link } from "react-router-dom";
import { cn } from "../ui";
import { ActionMenu, StatusPill, Tag, type ActionMenuItem } from "../page-kit";
import type { CLITool } from "../../cli-tools/constants";
import type { CLIToolStatus } from "./api";
import { TOOL_STATE_TONE, type ToolState } from "./toolState";

export type ToolView = {
  id: string;
  tool: CLITool;
  description: string;
  state: ToolState;
  status?: CLIToolStatus;
  autoApply: boolean;
  configPath: string;
  busy: boolean;
};

export type ToolHandlers = {
  open: (view: ToolView) => void;
  copyBaseUrl: () => void;
  copyPath: (view: ToolView) => void;
  reset: (view: ToolView) => void;
};

type ItemProps = { view: ToolView; handlers: ToolHandlers; canReset: boolean };

function stop(event: { stopPropagation: () => void }) {
  event.stopPropagation();
}

function openOnKey(event: KeyboardEvent<HTMLElement>, open: () => void) {
  if (event.target !== event.currentTarget) return;
  if (event.key === "Enter" || event.key === " ") {
    event.preventDefault();
    open();
  }
}

export function ToolIcon({ tool, size = "md" }: { tool: CLITool; size?: "md" | "lg" }) {
  return (
    // Brand tint comes from the tool catalog; it identifies the product, not a state.
    <span className={cn("ct-icon", size === "lg" && "is-lg")} style={{ color: tool.color }} aria-hidden>
      <span className="material-symbols-outlined">{tool.icon}</span>
    </span>
  );
}

export function ToolStatePill({ state, status }: { state: ToolState; status?: CLIToolStatus | null }) {
  const { t } = useTranslation();
  return (
    <StatusPill
      tone={TOOL_STATE_TONE[state]}
      loading={state === "checking"}
      title={state === "other" && status?.endpoint ? status.endpoint : t(`cliTools.stateHint.${state}`)}
    >
      {t(`cliTools.state.${state}`)}
    </StatusPill>
  );
}

export function SetupTypeTag({ tool, autoApply }: { tool: CLITool; autoApply: boolean }) {
  const { t } = useTranslation();
  if (tool.configType === "mitm") return <Tag tone="neutral">{t("cliTools.mitmBadge")}</Tag>;
  return autoApply ? (
    <Tag title={t("cliTools.kind.autoHint")}>{t("cliTools.kind.auto")}</Tag>
  ) : (
    <Tag tone="neutral" title={t("cliTools.kind.manualHint")}>{t("cliTools.kind.manual")}</Tag>
  );
}

/** The one line that tells the operator where the tool stands. */
function detailText(view: ToolView, t: TFunction) {
  const endpoint = view.status?.endpoint;
  if ((view.state === "connected" || view.state === "other") && endpoint) {
    const model = view.status?.model || view.status?.models?.[0];
    return { mono: true, text: model ? `${endpoint} · ${model}` : endpoint };
  }
  return { mono: false, text: t(`cliTools.stateHint.${view.state}`) };
}

function menuItems(view: ToolView, handlers: ToolHandlers, canReset: boolean, t: TFunction): ActionMenuItem[] {
  if (view.state === "unsupported") {
    return [{ key: "open", icon: "open_in_new", label: t("cliTools.menu.viewDetails"), onSelect: () => handlers.open(view) }];
  }
  const configured = Boolean(view.status?.has_tproxy || view.status?.has_9router);
  return [
    { key: "open", icon: "tune", label: t("cliTools.menu.openSetup"), onSelect: () => handlers.open(view) },
    { key: "base", icon: "link", label: t("cliTools.menu.copyBaseUrl"), hint: t("cliTools.menu.copyBaseUrlHint"), onSelect: handlers.copyBaseUrl },
    view.configPath
      ? { key: "path", icon: "description", label: t("cliTools.menu.copyConfigPath"), hint: view.configPath, onSelect: () => handlers.copyPath(view) }
      : null,
    view.tool.docsUrl
      ? {
          key: "docs",
          icon: "menu_book",
          label: t("cliTools.menu.docs"),
          hint: view.tool.docsUrl.replace(/^https?:\/\//, ""),
          onSelect: () => window.open(view.tool.docsUrl, "_blank", "noopener,noreferrer"),
        }
      : null,
    view.autoApply && canReset
      ? {
          key: "reset",
          icon: "restart_alt",
          label: t("cliTools.menu.reset"),
          hint: configured ? t("cliTools.menu.resetHint") : t("cliTools.menu.resetNothing"),
          danger: true,
          disabled: view.busy || !configured,
          onSelect: () => handlers.reset(view),
        }
      : null,
  ].filter(Boolean) as ActionMenuItem[];
}

function NextAction({ view }: { view: ToolView }) {
  const { t } = useTranslation();
  return (
    <Link
      to={`/cli-tools/${view.id}`}
      className={cn("ct-cta", `cp-tone-${TOOL_STATE_TONE[view.state]}`)}
      // The card navigates on click too; let the link do it once.
      onClick={stop}
      onKeyDown={stop}
      title={t(`cliTools.stateHint.${view.state}`)}
    >
      <span>{t(`cliTools.next.${view.state}`)}</span>
      <span className="material-symbols-outlined" aria-hidden>arrow_forward</span>
    </Link>
  );
}

function Actions({ view, handlers, canReset }: ItemProps) {
  const { t } = useTranslation();
  return (
    <div className="ct-actions" onClick={stop}>
      <NextAction view={view} />
      <ActionMenu label={t("cliTools.menu.more", { name: view.tool.name })} items={menuItems(view, handlers, canReset, t)} />
    </div>
  );
}

export function ToolCard({ view, handlers, canReset }: ItemProps) {
  const { t } = useTranslation();
  const detail = detailText(view, t);
  return (
    <article
      className={cn("cp-card ct-card", `ct-state-${view.state}`)}
      role="button"
      tabIndex={0}
      aria-label={`${view.tool.name} — ${t(`cliTools.state.${view.state}`)}`}
      onClick={() => handlers.open(view)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view))}
    >
      <div className="cp-card-head">
        <ToolIcon tool={view.tool} />
        <div className="cp-card-title">
          <strong title={view.tool.name}>{view.tool.name}</strong>
          <div className="cp-card-tags">
            <ToolStatePill state={view.state} status={view.status} />
            <SetupTypeTag tool={view.tool} autoApply={view.autoApply} />
          </div>
        </div>
      </div>
      <div className="cp-card-body">
        <p className="ct-desc">{view.description}</p>
        <p className={cn("ct-detail", detail.mono && "is-mono")} title={detail.text}>
          {detail.text}
        </p>
      </div>
      <div className="cp-card-foot">
        <span className="cp-card-meta">
          {view.configPath ? (
            <span title={view.configPath}>
              <span className="material-symbols-outlined" aria-hidden>description</span>
              {view.configPath}
            </span>
          ) : view.tool.mitmDomain ? (
            <span title={view.tool.mitmDomain}>
              <span className="material-symbols-outlined" aria-hidden>dns</span>
              {view.tool.mitmDomain}
            </span>
          ) : !view.autoApply ? (
            <span>{t("cliTools.configInApp")}</span>
          ) : null}
        </span>
        <Actions view={view} handlers={handlers} canReset={canReset} />
      </div>
    </article>
  );
}

export function ToolListRow({ view, handlers, canReset }: ItemProps) {
  const { t } = useTranslation();
  const detail = detailText(view, t);
  return (
    <div
      className={cn("cp-list-row ct-row", `ct-state-${view.state}`)}
      role="button"
      tabIndex={0}
      aria-label={`${view.tool.name} — ${t(`cliTools.state.${view.state}`)}`}
      onClick={() => handlers.open(view)}
      onKeyDown={(event) => openOnKey(event, () => handlers.open(view))}
    >
      <div className="ct-row-tool">
        <ToolIcon tool={view.tool} />
        <div className="cp-cell-main">
          <strong>{view.tool.name}</strong>
          <small title={view.description}>{view.description}</small>
        </div>
      </div>
      <div className="cp-cell">
        <ToolStatePill state={view.state} status={view.status} />
      </div>
      <div className={cn("cp-cell ct-row-detail", detail.mono && "is-mono")} title={detail.text}>
        {detail.text}
      </div>
      <div className="cp-cell cp-cell-mono ct-row-path" title={view.configPath || undefined}>
        {view.configPath || view.tool.mitmDomain || "—"}
      </div>
      <div className="cp-cell">
        <SetupTypeTag tool={view.tool} autoApply={view.autoApply} />
      </div>
      <Actions view={view} handlers={handlers} canReset={canReset} />
    </div>
  );
}
