import { isValidElement, memo } from "react";
import type { ComponentProps, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useCopyToClipboard } from "../../hooks/useCopyToClipboard";

type Props = {
  content: string;
  streaming?: boolean;
};

export const ChatMarkdown = memo(function ChatMarkdown({ content, streaming = false }: Props) {
  const text = content.trimEnd();
  if (!text) {
    return streaming ? <span className="ch-cursor" aria-hidden /> : null;
  }

  return (
    <div className="ch-markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
        {text}
      </ReactMarkdown>
      {streaming ? <span className="ch-cursor" aria-hidden /> : null}
    </div>
  );
});

/** Plain text of a rendered <code> child, for the copy button. */
function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return "";
}

function CodeBlock({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const { copied, copy } = useCopyToClipboard();
  const child = Array.isArray(children) ? children[0] : children;
  const className = isValidElement<{ className?: string }>(child) ? child.props.className || "" : "";
  const language = /language-([\w+-]+)/.exec(className)?.[1] || "";
  const code = nodeText(children).replace(/\n$/, "");
  return (
    <div className="ch-code">
      <div className="ch-code-head">
        <span>{language || t("chat.code")}</span>
        <button type="button" onClick={() => copy(code, "code")} aria-label={t("chat.copyCode")}>
          <span className="material-symbols-outlined" aria-hidden>{copied ? "check" : "content_copy"}</span>
          {copied ? t("common.copied") : t("common.copy")}
        </button>
      </div>
      <pre className="custom-scrollbar">{children}</pre>
    </div>
  );
}

const markdownComponents: ComponentProps<typeof ReactMarkdown>["components"] = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  ),
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  code: ({ className, children, ...props }) => {
    const isBlock = Boolean(className) || String(children).includes("\n");
    if (isBlock) {
      return (
        <code className={[className, "ch-code-block"].filter(Boolean).join(" ")} {...props}>
          {children}
        </code>
      );
    }
    return (
      <code className="ch-code-inline" {...props}>
        {children}
      </code>
    );
  },
  table: ({ children }) => (
    <div className="ch-table-wrap custom-scrollbar">
      <table>{children}</table>
    </div>
  ),
};
