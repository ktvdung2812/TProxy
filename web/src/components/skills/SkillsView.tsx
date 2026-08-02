import { useState } from "react";
import { Button, Card } from "../ui";

export type AgentSkill = {
  id: string;
  name: string;
  description: string;
  path: string;
  icon: string;
};

/** Static catalog — mirrors repo skills/ folder. */
export const AGENT_SKILLS: AgentSkill[] = [
  {
    id: "tproxy",
    name: "tproxy (entry)",
    description: "Setup TPROXY_URL / TPROXY_KEY and index of capability skills.",
    path: "skills/tproxy/SKILL.md",
    icon: "hub",
  },
  {
    id: "tproxy-chat",
    name: "Chat / code-gen",
    description: "OpenAI chat completions, Anthropic messages, Responses API.",
    path: "skills/tproxy-chat/SKILL.md",
    icon: "chat",
  },
  {
    id: "tproxy-image",
    name: "Image generation",
    description: "/v1/images/generations and edits.",
    path: "skills/tproxy-image/SKILL.md",
    icon: "image",
  },
  {
    id: "tproxy-video",
    name: "Video generation",
    description: "/v1/videos endpoints and job polling.",
    path: "skills/tproxy-video/SKILL.md",
    icon: "movie",
  },
  {
    id: "tproxy-tts",
    name: "Text-to-speech",
    description: "/v1/audio/speech for TTS providers.",
    path: "skills/tproxy-tts/SKILL.md",
    icon: "record_voice_over",
  },
  {
    id: "tproxy-stt",
    name: "Speech-to-text",
    description: "/v1/audio/transcriptions multipart STT.",
    path: "skills/tproxy-stt/SKILL.md",
    icon: "mic",
  },
  {
    id: "tproxy-embeddings",
    name: "Embeddings",
    description: "/v1/embeddings for vector models.",
    path: "skills/tproxy-embeddings/SKILL.md",
    icon: "polyline",
  },
  {
    id: "tproxy-web-search",
    name: "Web search",
    description: "/v1/search (Tavily and compatible).",
    path: "skills/tproxy-web-search/SKILL.md",
    icon: "travel_explore",
  },
  {
    id: "tproxy-web-fetch",
    name: "Web fetch",
    description: "URL → content patterns via gateway-safe tools.",
    path: "skills/tproxy-web-fetch/SKILL.md",
    icon: "link",
  },
];

function pastePrompt(skill: AgentSkill): string {
  return `Read this skill and use it with tproxy: ${skill.path}\n\nSet TPROXY_URL and TPROXY_KEY if not already configured.`;
}

export function SkillsView() {
  const [copied, setCopied] = useState<string>("");

  const copy = async (skill: AgentSkill) => {
    try {
      await navigator.clipboard.writeText(pastePrompt(skill));
      setCopied(skill.id);
      window.setTimeout(() => setCopied(""), 2000);
    } catch {
      /* ignore */
    }
  };

  return (
    <section className="section">
      <div className="section-head">
        <div>
          <p className="eyebrow">Agent integration</p>
          <h2>Skills</h2>
          <p>
            Drop-in skill files for Claude, Cursor, ChatGPT, and other agents. Paste a prompt so the agent loads the skill and routes
            work through your local tproxy gateway.
          </p>
        </div>
        <span className="meta">{AGENT_SKILLS.length} skills</span>
      </div>

      <Card pad="md" className="skills-intro">
        <p>
          Skills live in the repo under <code>skills/</code>. They are agent instructions (not runtime plugins). Prefer OAuth, API keys,
          or web cookies in <strong>Providers</strong> — MITM capture is not supported by tproxy.
        </p>
        <pre className="skills-env-block">{`export TPROXY_URL="http://127.0.0.1:28120"
export TPROXY_KEY="sk-..."`}</pre>
      </Card>

      <div className="cli-tools-grid skills-grid">
        {AGENT_SKILLS.map((skill) => (
          <Card key={skill.id} pad="sm" className="cli-tool-card">
            <div className="cli-tool-card-head">
              <span className="cli-tool-icon" style={{ color: "var(--color-brand-500)" }}>
                <span className="material-symbols-outlined">{skill.icon}</span>
              </span>
              <div className="cli-tool-card-body">
                <div className="cli-tool-title-row">
                  <h3>{skill.name}</h3>
                </div>
                <code className="skills-path">{skill.path}</code>
              </div>
            </div>
            <p className="cli-tool-desc">{skill.description}</p>
            <Button
              variant="outline"
              size="sm"
              icon={copied === skill.id ? "check" : "content_copy"}
              onClick={() => void copy(skill)}
            >
              {copied === skill.id ? "Copied prompt" : "Copy agent prompt"}
            </Button>
          </Card>
        ))}
      </div>
    </section>
  );
}
