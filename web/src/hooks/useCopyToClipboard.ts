import { useCallback, useRef, useState } from "react";

export function useCopyToClipboard(resetDelay = 2000) {
  const [copied, setCopied] = useState<string | null>(null);
  const timeoutRef = useRef<number | null>(null);

  const copy = useCallback((text: string, id = "default") => {
    const write = async () => {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        return;
      }
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
    };
    void write();
    setCopied(id);
    if (timeoutRef.current) {
      window.clearTimeout(timeoutRef.current);
    }
    timeoutRef.current = window.setTimeout(() => setCopied(null), resetDelay);
  }, [resetDelay]);

  return { copied, copy };
}
