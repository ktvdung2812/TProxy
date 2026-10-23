import { useMemo, type ReactNode } from "react";

// Keys, literals and redaction markers get their own class so long bodies
// scan quickly; everything else keeps the base text colour.
const TOKEN = /("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;

function highlight(line: string): ReactNode[] {
  const parts: ReactNode[] = [];
  let last = 0;
  for (const match of line.matchAll(TOKEN)) {
    const index = match.index ?? 0;
    if (index > last) parts.push(line.slice(last, index));
    const [whole, string, colon, literal, number] = match;
    if (string && colon) {
      parts.push(<span key={index} className="dg-tok-key">{string}</span>, colon);
    } else if (string) {
      parts.push(<span key={index} className={string === '"[REDACTED]"' ? "dg-tok-redacted" : "dg-tok-string"}>{string}</span>);
    } else if (literal || number) {
      parts.push(<span key={index} className="dg-tok-literal">{whole}</span>);
    }
    last = index + whole.length;
  }
  if (last < line.length) parts.push(line.slice(last));
  return parts;
}

/**
 * Read-only, line-numbered JSON block. Each line is a block element and the
 * numbers are CSS counters, so wrapped lines stay indented and selecting text
 * never copies the numbers.
 */
export function JsonView({ text, label }: { text: string; label: string }) {
  const lines = useMemo(() => text.split("\n").map(highlight), [text]);
  return (
    <pre className="dg-code custom-scrollbar" aria-label={label} tabIndex={0}>
      <code>
        {lines.map((parts, index) => (
          <span key={index} className="dg-line">{parts}</span>
        ))}
      </code>
    </pre>
  );
}
