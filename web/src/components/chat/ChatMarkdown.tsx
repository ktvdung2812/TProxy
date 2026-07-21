import type { ComponentProps } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

type Props = {
  content: string;
  streaming?: boolean;
};

export function ChatMarkdown({ content, streaming = false }: Props) {
  const text = content.trimEnd();
  if (!text) {
    return streaming ? <span className="chat-cursor">▋</span> : null;
  }

  return (
    <div className="chat-markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
        {text}
      </ReactMarkdown>
      {streaming ? <span className="chat-cursor">▋</span> : null}
    </div>
  );
}

const markdownComponents: ComponentProps<typeof ReactMarkdown>["components"] = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  ),
  pre: ({ children }) => <pre className="chat-md-pre">{children}</pre>,
  code: ({ className, children, ...props }) => {
    const isBlock = Boolean(className) || String(children).includes("\n");
    if (isBlock) {
      return (
        <code className={cnInline(className, "chat-md-code-block")} {...props}>
          {children}
        </code>
      );
    }
    return (
      <code className="chat-md-code-inline" {...props}>
        {children}
      </code>
    );
  },
  table: ({ children }) => (
    <div className="chat-md-table-wrap">
      <table>{children}</table>
    </div>
  ),
};

function cnInline(...parts: Array<string | undefined | false>) {
  return parts.filter(Boolean).join(" ");
}
