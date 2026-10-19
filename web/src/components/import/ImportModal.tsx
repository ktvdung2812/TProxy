import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "../ui";
import { storeApiKeySecretsFrom9routerBackup } from "../../lib/apiKeySecrets";
import { import9routerBackup, importCliproxyAuth, importSub2apiExport, type ImportSub2apiResult } from "./api";
import { countSub2apiAccounts, detectImportSource, type ImportSource } from "./utils";

type Props = {
  open: boolean;
  secret: string;
  initialSource?: ImportSource;
  onClose: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
  onMutated?: () => void;
};

type Sub2apiFilePlan = {
  file: File;
  payload: unknown;
  total: number;
  importCount: number;
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
  {
    id: "sub2api",
    label: "sub2api",
    title: "import.sourceSub2apiTitle",
    description: "import.sourceSub2apiDesc",
    patterns: ["*_sub2api_*.json", "*_sub2api_accounts.json", "accounts-export.json"],
  },
];

export function ImportModal({ open, secret, initialSource = "9router", onClose, onNotice, onError, onMutated }: Props) {
  const { t } = useTranslation();
  const inputRef = useRef<HTMLInputElement>(null);
  const [source, setSource] = useState<ImportSource>("9router");
  const [importing, setImporting] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [lastSummary, setLastSummary] = useState<string | null>(null);
  const [pendingDryRun, setPendingDryRun] = useState(false);
  const [sub2apiPlans, setSub2apiPlans] = useState<Sub2apiFilePlan[] | null>(null);
  const [skipDuplicates, setSkipDuplicates] = useState(true);

  const busy = importing || previewing;
  const active = SOURCES.find((item) => item.id === source) ?? SOURCES[0];
  const sub2apiBatch = source === "sub2api" && sub2apiPlans !== null && sub2apiPlans.length > 0;

  useEffect(() => {
    if (!open) return;
    setSource(initialSource);
    setSelectedFile(null);
    setLastSummary(null);
    setImporting(false);
    setPreviewing(false);
    setPendingDryRun(false);
    setSub2apiPlans(null);
    setSkipDuplicates(true);
    if (inputRef.current) {
      inputRef.current.value = "";
    }
  }, [open, initialSource]);

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
    if (nextSource === "sub2api") {
      const parts = [
        `${counts.providers ?? 0} providers`,
        `${counts.credentials ?? 0} credentials`,
        `${counts.proxy_pools ?? 0} proxy pools`,
      ];
      if ((counts.skipped ?? 0) > 0) {
        parts.push(`${counts.skipped} skipped`);
      }
      return parts.join(" · ");
    }
    return [`${counts.providers ?? 0} providers`, `${counts.credentials ?? 0} credentials`].join(" · ");
  };

  const runSingleImport = async (file: File, dryRun: boolean, forcedSource?: ImportSource) => {
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
          : nextSource === "sub2api"
            ? await importSub2apiExport(secret, payload, { dryRun, skipDuplicates })
            : await importCliproxyAuth(secret, payload, dryRun);
      const summary = formatSummary(nextSource, result.counts as Record<string, number>);
      setLastSummary(summary);
      setSelectedFile(file);
      const duplicateNotes =
        nextSource === "sub2api"
          ? ((result as ImportSub2apiResult).accounts ?? [])
              .filter((row) => row.action === "skip_duplicate")
              .map((row) => t("import.sub2apiDuplicateLine", { email: row.email || row.label }))
          : [];
      const warnings = [...(result.warnings ?? []), ...duplicateNotes];
      if (warnings.length) {
        onError?.(warnings.join("\n"));
      }
      if (!result.ok) {
        onError?.(result.errors?.join("\n") || t("import.failed"));
        return;
      }
      const sourceLabel =
        nextSource === "9router" ? "9router backup" : nextSource === "sub2api" ? "sub2api export" : "CLIProxyAPI auth";
      onNotice?.(dryRun ? t("import.previewReady", { summary }) : t("import.imported", { source: sourceLabel, summary }));
      if (!dryRun) {
        if (nextSource === "9router") {
          storeApiKeySecretsFrom9routerBackup(payload);
        }
        onMutated?.();
        onClose();
      }
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("import.failedFile"));
    } finally {
      setBusy(false);
      setPendingDryRun(false);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
    }
  };

  const runSub2apiBatch = async (plans: Sub2apiFilePlan[], dryRun: boolean) => {
    const setBusy = dryRun ? setPreviewing : setImporting;
    setBusy(true);
    setLastSummary(null);
    try {
      let totalProviders = 0;
      let totalCredentials = 0;
      let totalPools = 0;
      let totalSkipped = 0;
      const messages: string[] = [];
      for (const plan of plans) {
        if (plan.importCount <= 0) continue;
        const result = await importSub2apiExport(secret, plan.payload, {
          dryRun,
          maxAccounts: plan.importCount,
          skipDuplicates,
        });
        totalProviders += result.counts.providers ?? 0;
        totalCredentials += result.counts.credentials ?? 0;
        totalPools += result.counts.proxy_pools ?? 0;
        totalSkipped += result.counts.skipped ?? 0;
        if (result.warnings?.length) {
          messages.push(...result.warnings.map((line) => `${plan.file.name}: ${line}`));
        }
        for (const row of result.accounts ?? []) {
          if (row.action === "skip_duplicate") {
            messages.push(
              `${plan.file.name}: ${t("import.sub2apiDuplicateLine", { email: row.email || row.label })}`,
            );
          }
        }
        if (!result.ok) {
          onError?.(result.errors?.join("\n") || t("import.failed"));
          return;
        }
      }
      const summary = formatSummary("sub2api", {
        providers: totalProviders,
        credentials: totalCredentials,
        proxy_pools: totalPools,
        skipped: totalSkipped,
      });
      setLastSummary(summary);
      if (messages.length) {
        onError?.(messages.join("\n"));
      }
      onNotice?.(
        dryRun
          ? t("import.sub2apiBatchPreview", { files: plans.length, summary })
          : t("import.sub2apiBatchDone", { files: plans.length, summary }),
      );
      if (!dryRun) {
        onMutated?.();
        onClose();
      }
    } catch (error) {
      onError?.(error instanceof Error ? error.message : t("import.failedFile"));
    } finally {
      setBusy(false);
      setPendingDryRun(false);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
    }
  };

  const handleFiles = async (files: FileList | null, dryRun: boolean) => {
    if (!files?.length) return;
    if (source !== "sub2api") {
      await runSingleImport(files[0]!, dryRun);
      return;
    }
    const plans: Sub2apiFilePlan[] = [];
    for (const file of Array.from(files)) {
      const text = await file.text();
      const payload = JSON.parse(text) as unknown;
      const detected = detectImportSource(payload);
      if (detected && detected !== "sub2api") {
        onError?.(t("import.sub2apiWrongFormat", { name: file.name }));
        return;
      }
      const total = countSub2apiAccounts(payload);
      if (total === 0) {
        onError?.(t("import.sub2apiNoAccounts", { name: file.name }));
        return;
      }
      plans.push({ file, payload, total, importCount: Math.min(total, 1) });
    }
    if (plans.length === 1 && plans[0]!.total === 1) {
      await runSingleImport(plans[0]!.file, dryRun, "sub2api");
      return;
    }
    setSub2apiPlans(plans);
    setPendingDryRun(dryRun);
  };

  const openFilePicker = (dryRun: boolean) => {
    setPendingDryRun(dryRun);
    inputRef.current?.click();
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("import.title")}
      subtitle={t("import.subtitle")}
      icon="upload"
      size="md"
      className="import-modal"
      footer={
        sub2apiBatch ? (
          <>
            <Button variant="ghost" size="sm" onClick={() => setSub2apiPlans(null)} disabled={busy}>
              {t("import.back")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              icon="preview"
              disabled={busy}
              onClick={() => void runSub2apiBatch(sub2apiPlans, true)}
            >
              {previewing ? t("import.previewing") : t("import.preview")}
            </Button>
            <Button
              variant="primary"
              size="sm"
              icon="upload"
              disabled={busy}
              onClick={() => {
                setPendingDryRun(false);
                void runSub2apiBatch(sub2apiPlans, false);
              }}
            >
              {importing ? t("import.importing") : t("import.import")}
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
              {t("import.cancel")}
            </Button>
            <Button variant="outline" size="sm" icon="preview" disabled={busy} onClick={() => openFilePicker(true)}>
              {previewing ? t("import.previewing") : t("import.preview")}
            </Button>
            <Button variant="primary" size="sm" icon="upload" disabled={busy} onClick={() => openFilePicker(false)}>
              {importing ? t("import.importing") : t("import.import")}
            </Button>
          </>
        )
      }
    >
      <input
        ref={inputRef}
        type="file"
        accept="application/json,.json"
        multiple={source === "sub2api"}
        hidden
        onChange={(event) => {
          void handleFiles(event.target.files, pendingDryRun);
        }}
      />

      <div className="import-modal-tabs" role="tablist" aria-label={t("import.importSource")}>
        {SOURCES.map((item) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={source === item.id}
            className={`import-modal-tab${source === item.id ? " active" : ""}`}
            onClick={() => {
              setSource(item.id);
              setSub2apiPlans(null);
            }}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="import-modal-panel">
        <h4>{t(active.title)}</h4>
        <p>{t(active.description)}</p>
        {source === "sub2api" ? <p className="import-modal-hint">{t("import.sub2apiMultiFileHint")}</p> : null}
        <div className="import-modal-patterns">
          {active.patterns.map((pattern) => (
            <code key={pattern}>{pattern}</code>
          ))}
        </div>

        {sub2apiBatch ? (
          <div className="import-modal-batch">
            <p>{t("import.sub2apiBatchIntro")}</p>
            <label className="import-modal-checkbox">
              <input
                type="checkbox"
                checked={skipDuplicates}
                onChange={(event) => setSkipDuplicates(event.target.checked)}
              />
              {t("import.sub2apiSkipDuplicates")}
            </label>
            <table className="import-modal-batch-table">
              <thead>
                <tr>
                  <th>{t("import.sub2apiBatchFile")}</th>
                  <th>{t("import.sub2apiBatchFound")}</th>
                  <th>{t("import.sub2apiBatchImportCount")}</th>
                </tr>
              </thead>
              <tbody>
                {sub2apiPlans!.map((plan, index) => (
                  <tr key={plan.file.name}>
                    <td>{plan.file.name}</td>
                    <td>{plan.total}</td>
                    <td>
                      <input
                        type="number"
                        min={1}
                        max={plan.total}
                        value={plan.importCount}
                        onChange={(event) => {
                          const next = Math.min(plan.total, Math.max(1, Number(event.target.value) || 1));
                          setSub2apiPlans((current) =>
                            current?.map((row, rowIndex) =>
                              rowIndex === index ? { ...row, importCount: next } : row,
                            ) ?? null,
                          );
                        }}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <button
            type="button"
            className="import-modal-dropzone"
            disabled={busy}
            onClick={() => openFilePicker(false)}
          >
            <span className="material-symbols-outlined" aria-hidden>
              upload_file
            </span>
            <strong>
              {selectedFile
                ? selectedFile.name
                : source === "sub2api"
                  ? t("import.sub2apiChooseFiles")
                  : t("import.chooseFile")}
            </strong>
            <span>{t("import.autoDetect")}</span>
          </button>
        )}
        {lastSummary ? (
          <p className="import-modal-summary">
            {t("import.lastResult")} {lastSummary}
          </p>
        ) : null}
      </div>
    </Modal>
  );
}
