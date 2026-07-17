import { useState } from "react";
import { Button, Modal } from "../ui";

export type ManualConfigEntry = {
  filename: string;
  content: string;
};

type Props = {
  open: boolean;
  onClose: () => void;
  title?: string;
  configs: ManualConfigEntry[];
};

export function ManualConfigModal({ open, onClose, title = "Manual configuration", configs }: Props) {
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null);

  const copyConfig = async (text: string, index: number) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedIndex(index);
      window.setTimeout(() => setCopiedIndex(null), 2000);
    } catch {
      /* clipboard may be unavailable */
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={title} size="lg" icon="content_copy">
      <div className="manual-config-list">
        {configs.map((config, index) => (
          <div key={`${config.filename}-${index}`} className="manual-config-item">
            <div className="manual-config-head">
              <span className="manual-config-filename">{config.filename}</span>
              <Button
                variant="ghost"
                size="sm"
                icon={copiedIndex === index ? "check" : "content_copy"}
                onClick={() => void copyConfig(config.content, index)}
              >
                {copiedIndex === index ? "Copied" : "Copy"}
              </Button>
            </div>
            <pre className="manual-config-pre">
              <code>{config.content}</code>
            </pre>
          </div>
        ))}
      </div>
    </Modal>
  );
}
