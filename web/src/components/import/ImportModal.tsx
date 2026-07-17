import { useEffect, useRef, useState } from "react";
import { Button, Modal } from "../ui";
import { import9routerBackup, importCliproxyAuth } from "./api";
import { detectImportSource, type ImportSource } from "./utils";

type Props = {
  open: boolean;
  secret: string;
  onClose: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
  onMutated?: () => void;
};

const SOURCES: Array<{
  id: ImportSource;
  label: string;
  title: string;
  description: string;
  patterns: string[];
}> = [
  {
    id: "9router",
    label: "9router",
    title: "9router backup",
    description:
      "Migrate provider credentials, client API keys, combos, and virtual models from a full 9router export.",
    patterns: ["9router-backup-*.json"],
  },
  {
    id: "cliproxy",
    label: "CLIProxyAPI",
    title: "CLIProxyAPI auth",
    description:
      "Import OAuth credentials exported from the CLIProxyAPI auth directory.",
    patterns: ["codex-*.json", "claude-*.json", "xai-*.json"],
  },
];

export function ImportModal({ open, secret, onClose, onNotice, onError, onMutated }: Props) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [source, setSource] = useState<ImportSource>("9router");
  const [importing, setImporting] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [lastSummary, setLastSummary] = useState<string | null>(null);
  const [pendingDryRun, setPendingDryRun] = useState(false);

  const busy = importing || previewing;
  const active = SOURCES.find((item) => item.id === source) ?? SOURCES[0];

  useEffect(() => {
    if (!open) return;
    setSource("9router");
    setSelectedFile(null);
    setLastSummary(null);
    setImporting(false);
    setPreviewing(false);
    setPendingDryRun(false);
    if (inputRef.current) {
      inputRef.current.value = "";
    }
  }, [open]);

  const formatSummary = (nextSource: ImportSource, counts: Record<string, number>) => {
    if (nextSource === "9router") {
      return [
        `${counts.providers ?? 0} providers`,
        `${counts.credentials ?? 0} credentials`,
        `${counts.api_keys ?? 0} API keys`,
        `${counts.models ?? 0} virtual models`,
        `${counts.combos ?? 0} combos`,
      ].join(" · ");
    }
    return [`${counts.providers ?? 0} providers`, `${counts.credentials ?? 0} credentials`].join(" · ");
  };

  const runImport = async (file: File, dryRun: boolean, forcedSource?: ImportSource) => {
    const setBusy = dryRun ? setPreviewing : setImporting;
    setBusy(true);
    setLastSummary(null);
    try {
      const text = await file.text();
      const payload = JSON.parse(text) as unknown;
      const detected = detectImportSource(payload);
      const nextSource = forcedSource ?? detected ?? source;
      if (detected && detected !== source && !forcedSource) {
        setSource(detected);
      }
      const result =
        nextSource === "9router"
          ? await import9routerBackup(secret, payload, dryRun)
          : await importCliproxyAuth(secret, payload, dryRun);
      const summary = formatSummary(nextSource, result.counts as Record<string, number>);
      setLastSummary(summary);
      setSelectedFile(file);
      if (result.warnings?.length) {
        onError?.(result.warnings.join("\n"));
      }
      if (!result.ok) {
        onError?.(result.errors?.join("\n") || "Import failed");
        return;
      }
      onNotice?.(
        dryRun
          ? `Preview ready: ${summary}`
          : `Imported ${nextSource === "9router" ? "9router backup" : "CLIProxyAPI auth"}: ${summary}`,
      );
      if (!dryRun) {
        onMutated?.();
        onClose();
      }
    } catch (error) {
      onError?.(error instanceof Error ? error.message : "Failed to import file");
    } finally {
      setBusy(false);
      setPendingDryRun(false);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
    }
  };

  const openFilePicker = (dryRun: boolean) => {
    setPendingDryRun(dryRun);
    inputRef.current?.click();
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Import data"
      subtitle="Migrate credentials and routing from 9router or CLIProxyAPI"
      icon="upload"
      size="md"
      className="import-modal"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="outline"
            size="sm"
            icon="preview"
            disabled={busy}
            onClick={() => openFilePicker(true)}
          >
            {previewing ? "Previewing…" : "Preview"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="upload"
            disabled={busy}
            onClick={() => openFilePicker(false)}
          >
            {importing ? "Importing…" : "Import"}
          </Button>
        </>
      }
    >
      <input
        ref={inputRef}
        type="file"
        accept="application/json,.json"
        hidden
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (!file) return;
          void runImport(file, pendingDryRun);
        }}
      />

      <div className="import-modal-tabs" role="tablist" aria-label="Import source">
        {SOURCES.map((item) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={source === item.id}
            className={`import-modal-tab${source === item.id ? " active" : ""}`}
            onClick={() => setSource(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="import-modal-panel">
        <h4>{active.title}</h4>
        <p>{active.description}</p>
        <div className="import-modal-patterns">
          {active.patterns.map((pattern) => (
            <code key={pattern}>{pattern}</code>
          ))}
        </div>
        <button
          type="button"
          className="import-modal-dropzone"
          disabled={busy}
          onClick={() => openFilePicker(false)}
        >
          <span className="material-symbols-outlined" aria-hidden>
            upload_file
          </span>
          <strong>{selectedFile ? selectedFile.name : "Choose JSON file"}</strong>
          <span>Auto-detects format when possible</span>
        </button>
        {lastSummary ? <p className="import-modal-summary">Last result: {lastSummary}</p> : null}
      </div>
    </Modal>
  );
}
