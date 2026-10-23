import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { Button, cn } from "../ui";
import {
  ActionMenu,
  EmptyPanel,
  FilterTabs,
  GuideToggle,
  IconAction,
  PageGuide,
  SearchField,
  SectionHead,
  Tag,
  Toolbar,
  ToolbarEnd,
  ToolbarRow,
  ToolButton,
  usePageGuide,
} from "../page-kit";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";
import { gatewayBaseUrl } from "../apis/utils";

export type SkillCategory = "setup" | "text" | "media" | "web";

export type AgentSkill = {
  id: string;
  name: string;
  description: string;
  path: string;
  icon: string;
  category: SkillCategory;
};

/** Static catalog — mirrors repo skills/ folder. Names and descriptions are translated under skills.items. */
export const AGENT_SKILLS: AgentSkill[] = [
  { id: "tproxy", name: "tproxy (entry)", description: "Setup TPROXY_URL / TPROXY_KEY and index of capability skills.", path: "skills/tproxy/SKILL.md", icon: "hub", category: "setup" },
  { id: "tproxy-chat", name: "Chat / code-gen", description: "OpenAI chat completions, Anthropic messages, Responses API.", path: "skills/tproxy-chat/SKILL.md", icon: "chat", category: "text" },
  { id: "tproxy-image", name: "Image generation", description: "/v1/images/generations and edits.", path: "skills/tproxy-image/SKILL.md", icon: "image", category: "media" },
  { id: "tproxy-video", name: "Video generation", description: "/v1/videos endpoints and job polling.", path: "skills/tproxy-video/SKILL.md", icon: "movie", category: "media" },
  { id: "tproxy-tts", name: "Text-to-speech", description: "/v1/audio/speech for TTS providers.", path: "skills/tproxy-tts/SKILL.md", icon: "record_voice_over", category: "media" },
  { id: "tproxy-stt", name: "Speech-to-text", description: "/v1/audio/transcriptions multipart STT.", path: "skills/tproxy-stt/SKILL.md", icon: "mic", category: "media" },
  { id: "tproxy-embeddings", name: "Embeddings", description: "/v1/embeddings for vector models.", path: "skills/tproxy-embeddings/SKILL.md", icon: "polyline", category: "text" },
  { id: "tproxy-web-search", name: "Web search", description: "/v1/search (Tavily and compatible).", path: "skills/tproxy-web-search/SKILL.md", icon: "travel_explore", category: "web" },
  { id: "tproxy-web-fetch", name: "Web fetch", description: "URL → content patterns via gateway-safe tools.", path: "skills/tproxy-web-fetch/SKILL.md", icon: "link", category: "web" },
];

const CATEGORIES: Array<{ value: SkillCategory; icon: string }> = [
  { value: "setup", icon: "rocket_launch" },
  { value: "text", icon: "notes" },
  { value: "media", icon: "perm_media" },
  { value: "web", icon: "language" },
];

const GITHUB_REPO = "ktvdung2812/TProxy";
const githubBlob = (skill: AgentSkill) => `https://github.com/${GITHUB_REPO}/blob/main/${skill.path}`;
const githubRaw = (skill: AgentSkill) => `https://raw.githubusercontent.com/${GITHUB_REPO}/main/${skill.path}`;

function pastePrompt(skill: AgentSkill): string {
  return `Read this skill and use it with tproxy: ${skill.path}\n\nSet TPROXY_URL and TPROXY_KEY if not already configured.`;
}

type Filter = SkillCategory | "all";

export function SkillsView() {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const guide = usePageGuide("skills");
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  const [notice, setNotice] = useState("");

  const name = (skill: AgentSkill) => t(`skills.items.${skill.id}.name`, { defaultValue: skill.name });
  const description = (skill: AgentSkill) => t(`skills.items.${skill.id}.description`, { defaultValue: skill.description });
  const envBlock = `export TPROXY_URL="${gatewayBaseUrl().replace(/\/v1\/?$/, "")}"\nexport TPROXY_KEY="sk-..."`;
  const entry = AGENT_SKILLS[0];

  const copyText = (text: string, id: string, label: string) => {
    copy(text, id);
    setNotice(t("skills.copiedNotice", { value: label }));
  };

  const searched = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return AGENT_SKILLS;
    return AGENT_SKILLS.filter((skill) =>
      [skill.id, skill.path, skill.name, skill.description, name(skill), description(skill)].some((value) => value.toLowerCase().includes(query)),
    );
    // name/description depend on the active language through t.
  }, [search, t]);
  const visible = filter === "all" ? searched : searched.filter((skill) => skill.category === filter);
  const count = (category: SkillCategory) => searched.filter((skill) => skill.category === category).length;

  return (
    <section className="cp-page sk-page">
      <Toolbar>
        <ToolbarRow>
          <FilterTabs
            label={t("skills.tabsLabel")}
            value={filter}
            onChange={(value) => setFilter(filter === value ? "all" : value)}
            items={[
              { value: "all", icon: "apps", label: t("skills.category.all"), count: searched.length },
              ...CATEGORIES.map((category) => ({
                value: category.value,
                icon: category.icon,
                label: t(`skills.category.${category.value}`),
                count: count(category.value),
              })),
            ]}
          />
          <ToolbarEnd>
            <Button
              variant="primary"
              size="sm"
              icon={copied === `prompt-${entry.id}` ? "check" : "content_copy"}
              title={t("skills.copySetupPromptHint")}
              onClick={() => copyText(pastePrompt(entry), `prompt-${entry.id}`, t("skills.card.copyPrompt"))}
            >
              {t("skills.copySetupPrompt")}
            </Button>
          </ToolbarEnd>
        </ToolbarRow>
        <ToolbarRow>
          <SearchField value={search} onChange={setSearch} placeholder={t("skills.searchPlaceholder")} />
          <ToolbarEnd>
            {notice && copied ? (
              <span className="sk-notice" role="status">
                <span className="material-symbols-outlined" aria-hidden>check</span>
                {notice}
              </span>
            ) : null}
            <GuideToggle open={guide.open} onToggle={guide.toggle} />
          </ToolbarEnd>
        </ToolbarRow>
      </Toolbar>

      <PageGuide
        open={guide.open}
        onDismiss={guide.dismiss}
        title={t("skills.guide.title")}
        steps={[
          { icon: "description", text: t("skills.guide.what") },
          { icon: "bolt", text: t("skills.guide.fast") },
          { icon: "key", text: t("skills.guide.env") },
        ]}
      />

      <section className="cp-panel sk-setup">
        <SectionHead
          leading={<span className="cp-section-icon material-symbols-outlined" aria-hidden>terminal</span>}
          title={t("skills.setup.title")}
          subtitle={t("skills.setup.subtitle")}
          actions={
            <ToolButton
              icon={copied === "env" ? "check" : "content_copy"}
              tone={copied === "env" ? "success" : undefined}
              onClick={() => copyText(envBlock, "env", "TPROXY_URL / TPROXY_KEY")}
            >
              {copied === "env" ? t("skills.setup.copied") : t("skills.setup.copy")}
            </ToolButton>
          }
        />
        <div className="sk-setup-body">
          <pre className="sk-env">{envBlock}</pre>
          <div className="sk-setup-notes">
            <p className="cp-note">
              <span className="material-symbols-outlined" aria-hidden>key</span>
              <Link to="/apis">{t("skills.setup.keyLink")}</Link>
            </p>
            <p className="cp-note">
              <span className="material-symbols-outlined" aria-hidden>info</span>
              <span>{t("skills.setup.note")}</span>
            </p>
          </div>
        </div>
      </section>

      {visible.length === 0 ? (
        <EmptyPanel
          icon="filter_alt_off"
          title={t("skills.noMatch")}
          description={t("skills.noMatchHint")}
          action={
            <Button
              variant="secondary"
              icon="filter_alt_off"
              onClick={() => {
                setSearch("");
                setFilter("all");
              }}
            >
              {t("skills.clearFilters")}
            </Button>
          }
        />
      ) : (
        <div className="cp-grid sk-grid" style={{ ["--cp-card-min" as string]: "280px" }}>
          {visible.map((skill) => {
            const promptId = `prompt-${skill.id}`;
            const isEntry = skill.category === "setup";
            return (
              <article key={skill.id} className={cn("cp-card sk-card", isEntry && "is-entry")}>
                <header className="cp-card-head">
                  <span className="sk-icon material-symbols-outlined" aria-hidden>{skill.icon}</span>
                  <div className="cp-card-title">
                    <strong title={name(skill)}>{name(skill)}</strong>
                    <div className="cp-card-tags">
                      <Tag tone={isEntry ? "primary" : "neutral"}>{t(`skills.category.${skill.category}`)}</Tag>
                      {isEntry ? <Tag tone="success">{t("skills.card.entry")}</Tag> : null}
                    </div>
                  </div>
                </header>
                <div className="cp-card-body">
                  <p className="sk-desc">{description(skill)}</p>
                </div>
                <footer className="cp-card-foot">
                  <div className="cp-card-meta">
                    <span title={skill.path}>
                      <span className="material-symbols-outlined" aria-hidden>description</span>
                      <span className="sk-path">{skill.path}</span>
                    </span>
                  </div>
                  <div className="sk-actions">
                    <IconAction
                      icon={copied === promptId ? "check" : "content_copy"}
                      label={copied === promptId ? t("skills.card.copied") : t("skills.card.copyPrompt")}
                      active={copied === promptId}
                      onClick={() => copyText(pastePrompt(skill), promptId, t("skills.card.copyPrompt"))}
                    />
                    <ActionMenu
                      label={t("skills.card.more")}
                      items={[
                        { key: "prompt", icon: "content_copy", label: t("skills.card.copyPrompt"), onSelect: () => copyText(pastePrompt(skill), promptId, t("skills.card.copyPrompt")) },
                        { key: "path", icon: "description", label: t("skills.card.copyPath"), hint: skill.path, onSelect: () => copyText(skill.path, `path-${skill.id}`, skill.path) },
                        { key: "raw", icon: "link", label: t("skills.card.copyLink"), hint: t("skills.card.copyLinkHint"), onSelect: () => copyText(githubRaw(skill), `raw-${skill.id}`, githubRaw(skill)) },
                        { key: "open", icon: "open_in_new", label: t("skills.card.openGithub"), onSelect: () => window.open(githubBlob(skill), "_blank", "noopener,noreferrer") },
                      ]}
                    />
                  </div>
                </footer>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}
