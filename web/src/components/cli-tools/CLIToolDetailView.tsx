import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate, useParams } from "react-router-dom";
import { getAnyTool, isAutoApplyTool } from "../../cli-tools/constants";
import { isLocalDashboardHost } from "../../devDefaults";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { buildModelOptions } from "../../lib/modelOptions";
import { Button, ConfirmDialog } from "../ui";
import { ActionMenu, EmptyPanel, GuideToggle, IconAction, PageGuide, ToolButton, usePageGuide, type ActionMenuItem } from "../page-kit";
import { resetCLIToolConfig } from "./api";
import { Note } from "./CopyBits";
import { MitmToolGuide } from "./MitmToolGuide";
import { SetupTypeTag, ToolIcon, ToolStatePill } from "./ToolSummaryCard";
import { ToolSetup } from "./ToolSetup";
import { useToolCopy } from "./toolCopy";
import { configState as resolveConfigState, toolConfigPath, toolState } from "./toolState";
import { useCliGateway } from "./useCliGateway";
import { useToolStatus } from "./useToolStatus";

type SnapshotSlice = {
  models: { ID: string; DisplayName?: string; Enabled?: boolean }[] | null;
  combos: { id: string; display_name?: string; enabled?: boolean }[] | null;
  api_keys: { id: string; name: string; enabled?: boolean }[] | null;
};

type Props = {
  snapshot: SnapshotSlice;
  secret: string;
};

function BackLink() {
  const { t } = useTranslation();
  return (
    <Link to="/cli-tools" className="ct-back">
      <span className="material-symbols-outlined" aria-hidden>arrow_back</span>
      {t("cliTools.backToCliTools")}
    </Link>
  );
}

export function CLIToolDetailView({ snapshot, secret }: Props) {
  const { toolId } = useParams<{ toolId: string }>();
  const tool = toolId ? getAnyTool(toolId) : undefined;

  if (!toolId) {
    return <Navigate to="/cli-tools" replace />;
  }
  if (!tool) {
    return <ToolNotFound />;
  }
  // Keyed so per-tool state (key, model, options) resets when switching tools.
  return <ToolDetail key={tool.id} toolId={tool.id} snapshot={snapshot} secret={secret} />;
}

function ToolNotFound() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <section className="cp-page ct-page">
      <BackLink />
      <EmptyPanel
        icon="search_off"
        title={t("cliTools.toolNotFound")}
        description={t("cliTools.toolNotFoundHint")}
        action={
          <Button variant="secondary" icon="arrow_back" onClick={() => navigate("/cli-tools")}>
            {t("cliTools.backToCliTools")}
          </Button>
        }
      />
    </section>
  );
}

function ToolDetail({ toolId, snapshot, secret }: Props & { toolId: string }) {
  const { t } = useTranslation();
  const tool = getAnyTool(toolId)!;
  const toolCopy = useToolCopy();
  const guide = usePageGuide("cli-tool-detail");
  const gateway = useCliGateway(secret);
  const { status, loading, reload } = useToolStatus(secret, tool.id);
  const { copy } = useCopyToClipboard();
  const [confirmReset, setConfirmReset] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [notice, setNotice] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const isMitm = tool.configType === "mitm";
  const autoApply = isAutoApplyTool(tool.id);
  const canAutoApply = autoApply && isLocalDashboardHost();
  const state = toolState(tool, status, gateway.knownBaseUrls, loading);
  const configState = resolveConfigState(status, gateway.knownBaseUrls);
  const configPath = isMitm ? "" : toolConfigPath(tool, status);
  const configured = Boolean(status?.has_tproxy || status?.has_9router);

  const models = useMemo(
    () => buildModelOptions(snapshot.models ?? [], snapshot.combos ?? [], tool.defaultModels),
    [snapshot.models, snapshot.combos, tool.defaultModels],
  );
  const apiKeys = useMemo(
    () =>
      (snapshot.api_keys ?? []).map((key) => ({
        id: key.id,
        name: key.name || key.id,
        enabled: key.enabled,
      })),
    [snapshot.api_keys],
  );

  const handleReset = async () => {
    setConfirmReset(false);
    setResetting(true);
    setNotice(null);
    try {
      await resetCLIToolConfig(secret, tool.id);
      setNotice({ tone: "success", text: t("cliTools.configReset") });
      await reload();
    } catch (error) {
      setNotice({ tone: "danger", text: String(error) });
    } finally {
      setResetting(false);
    }
  };

  const copyWithNotice = (value: string, text: string) => {
    copy(value, "menu");
    setNotice({ tone: "success", text });
  };

  const menuItems: ActionMenuItem[] = [
    !isMitm
      ? { key: "base", icon: "link", label: t("cliTools.menu.copyBaseUrl"), hint: gateway.baseUrl, onSelect: () => copyWithNotice(gateway.baseUrl, t("cliTools.notice.baseUrlCopied", { url: gateway.baseUrl })) }
      : null,
    configPath ? { key: "path", icon: "description", label: t("cliTools.menu.copyConfigPath"), hint: configPath, onSelect: () => copyWithNotice(configPath, t("cliTools.notice.pathCopied", { path: configPath })) } : null,
    canAutoApply
      ? {
          key: "reset",
          icon: "restart_alt",
          label: t("cliTools.menu.reset"),
          hint: configured ? t("cliTools.menu.resetHint") : t("cliTools.menu.resetNothing"),
          danger: true,
          disabled: resetting || !configured,
          onSelect: () => setConfirmReset(true),
        }
      : null,
  ].filter(Boolean) as ActionMenuItem[];

  const pointsAt = status?.endpoint;
  const currentModel = status?.model || status?.models?.join(", ");

  return (
    <section className="cp-page ct-page ct-detail-page">
      <BackLink />

      <header className="cp-panel ct-hero">
        <ToolIcon tool={tool} size="lg" />
        <div className="ct-hero-main">
          <div className="ct-hero-title">
            <h2>{tool.name}</h2>
            <ToolStatePill state={state} status={status} />
            <SetupTypeTag tool={tool} autoApply={autoApply} />
          </div>
          <p>{toolCopy.description(tool)}</p>
          {pointsAt || currentModel ? (
            <dl className="ct-hero-facts">
              {pointsAt ? (
                <div>
                  <dt>{t("cliTools.hero.pointsAt")}</dt>
                  <dd title={pointsAt}>{pointsAt}</dd>
                </div>
              ) : null}
              {currentModel ? (
                <div>
                  <dt>{t("cliTools.hero.model")}</dt>
                  <dd title={currentModel}>{currentModel}</dd>
                </div>
              ) : null}
            </dl>
          ) : (
            <p className="ct-hero-state">{t(`cliTools.stateHint.${state}`)}</p>
          )}
        </div>
        <div className="ct-hero-actions">
          {tool.docsUrl ? (
            <ToolButton icon="menu_book" onClick={() => window.open(tool.docsUrl, "_blank", "noopener,noreferrer")}>
              {t("cliTools.hero.docs")}
            </ToolButton>
          ) : null}
          {autoApply ? (
            <IconAction icon="refresh" label={t("cliTools.refreshStatus")} bordered spinning={loading} disabled={loading} onClick={() => void reload()} />
          ) : null}
          {menuItems.length > 0 ? <ActionMenu label={t("cliTools.menu.more", { name: tool.name })} items={menuItems} /> : null}
          <GuideToggle open={guide.open} onToggle={guide.toggle} />
        </div>
      </header>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={isMitm ? t("cliTools.detailGuide.mitmTitle") : t("cliTools.detailGuide.title", { name: tool.name })}
        steps={
          isMitm
            ? [
                { icon: "block", text: t("cliTools.detailGuide.mitmWhat") },
                { icon: "hub", text: t("cliTools.detailGuide.mitmProviders") },
                { icon: "swap_horiz", text: t("cliTools.detailGuide.mitmSlots") },
              ]
            : [
                { icon: "key", text: t("cliTools.detailGuide.values", { name: tool.name }) },
                { icon: "tune", text: t("cliTools.detailGuide.model") },
                {
                  icon: canAutoApply ? "bolt" : "content_copy",
                  text: canAutoApply ? t("cliTools.detailGuide.apply") : t("cliTools.detailGuide.copy"),
                },
              ]
        }
      />

      {notice ? (
        <div className="ct-notice">
          <Note tone={notice.tone}>{notice.text}</Note>
          <button type="button" className="cp-icon-btn" aria-label={t("common.dismiss")} title={t("common.dismiss")} onClick={() => setNotice(null)}>
            <span className="material-symbols-outlined" aria-hidden>close</span>
          </button>
        </div>
      ) : null}

      {isMitm ? (
        <MitmToolGuide tool={tool} models={models} />
      ) : (
        <ToolSetup
          tool={tool}
          models={models}
          apiKeys={apiKeys}
          secret={secret}
          gateway={gateway}
          status={status}
          statusLoading={loading}
          configState={configState}
          configPath={configPath}
          onApplied={reload}
        />
      )}

      {tool.docsUrl || tool.settingsFile ? (
        <p className="ct-foot-meta">
          {tool.settingsFile ? (
            <span>
              {t("cliTools.configFile")} <code>{tool.settingsFile}</code>
            </span>
          ) : null}
          {tool.docsUrl ? (
            <span>
              {t("cliTools.docs")}{" "}
              <a href={tool.docsUrl} target="_blank" rel="noreferrer">
                {tool.docsUrl}
              </a>
            </span>
          ) : null}
        </p>
      ) : null}

      <ConfirmDialog
        open={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={() => void handleReset()}
        title={t("cliTools.resetConfirmTitle", { name: tool.name })}
        message={t("cliTools.resetConfirmMessage", { name: tool.name, path: configPath || "—" })}
        confirmText={t("cliTools.menu.reset")}
        cancelText={t("common.cancel")}
        variant="danger"
      />
    </section>
  );
}
