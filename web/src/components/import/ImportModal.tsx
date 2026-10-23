import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Modal } from "../ui";
import { storeApiKeySecretsFrom9routerBackup } from "../../lib/apiKeySecrets";
import { importData, type ImportResult } from "./api";

type Props = {
  open: boolean;
  secret: string;
  onClose: () => void;
  onNotice?: (message: string) => void;
  onError?: (message: string) => void;
  onMutated?: () => void;
};

type FilePlan = {
  file: File;
  payload?: unknown;
  preview?: ImportResult;
  importCount?: number;
  reviewed?: boolean;
  imported?: boolean;
  error?: string;
};

const SOURCE_LABELS: Record<ImportResult["source"], string> = {
  "9router": "9router",
  cliproxy: "CLIProxyAPI",
  sub2api: "sub2api",
  chatgpt: "ChatGPT OAuth → Codex",
  "auth-bundle": "tproxy OAuth bundle",
};

function supportsAccountOptions(plan: FilePlan) {
  return plan.preview?.source === "sub2api" || plan.preview?.source === "chatgpt";
}

export function ImportModal({ open, secret, onClose, onNotice, onError, onMutated }: Props) {
  const { t } = useTranslation();
  const inputRef = useRef<HTMLInputElement>(null);
  const [plans, setPlans] = useState<FilePlan[]>([]);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [skipDuplicates, setSkipDuplicates] = useState(true);
  const pending = plans.filter((plan) => !plan.imported);
  const canImport = pending.length > 0 && pending.every((plan) => plan.reviewed && !plan.error);

  useEffect(() => {
    if (!open) return;
    setPlans([]);
    setBusy(null);
    setSkipDuplicates(true);
  }, [open]);

  const summary = (result: ImportResult) => Object.entries(result.result.counts)
    .filter(([, count]) => count > 0)
    .map(([key, count]) => t(`import.counts.${key}`, { count }))
    .join(" · ");

  const run = async (rows: FilePlan[], dryRun: boolean) => {
    setBusy(dryRun ? "preview" : "import");
    const next = [...rows];
    let attemptedImport = false;
    let importedFiles = 0;
    try {
      for (let index = 0; index < next.length; index++) {
        const row = next[index]!;
        if (row.imported) continue;
        try {
          if (row.file.size > 32 * 1024 * 1024) throw new Error(t("import.fileTooLarge"));
          let payload = row.payload;
          if (payload === undefined) {
            try {
              payload = JSON.parse(await row.file.text());
            } catch {
              throw new Error(t("import.invalidJSON"));
            }
          }
          attemptedImport ||= !dryRun;
          const preview = await importData(secret, payload, {
            dryRun, maxAccounts: row.importCount, skipDuplicates,
          });
          const hasRecords = Object.values(preview.result.counts).some((count) => count > 0);
          const error = !preview.result.ok
            ? preview.result.errors?.join("\n") || t("import.failed")
            : !hasRecords ? t("import.noRecords") : undefined;
          next[index] = {
            ...row, payload, preview, error,
            importCount: row.importCount ?? preview.total_accounts,
            reviewed: !error,
            imported: !dryRun && !error,
          };
          if (!dryRun && !error) {
            importedFiles++;
            if (preview.source === "9router") storeApiKeySecretsFrom9routerBackup(payload);
          }
        } catch (error) {
          next[index] = { ...row, reviewed: false, error: error instanceof Error ? error.message : t("import.failedFile") };
        }
        setPlans([...next]);
      }
      if (!dryRun && importedFiles) onNotice?.(t("import.batchDone", { count: importedFiles }));
      if (next.some((row) => row.error)) onError?.(t("import.checkFileErrors"));
    } finally {
      if (attemptedImport) onMutated?.();
      setBusy(null);
    }
  };

  const selectFiles = (files: FileList | null) => {
    if (!files?.length || busy) return;
    const next = Array.from(files, (file): FilePlan => ({ file }));
    setPlans(next);
    void run(next, true);
  };

  return (
    <Modal
      open={open}
      onClose={() => { if (!busy) onClose(); }}
      title={t("import.title")}
      subtitle={t("import.subtitle")}
      icon="upload"
      size="lg"
      className="import-modal"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose} disabled={!!busy}>{t("import.close")}</Button>
          <Button variant="outline" size="sm" icon="preview" aria-label={t("import.preview")} disabled={!!busy || !pending.length} onClick={() => void run(plans, true)}>
            {busy === "preview" ? t("import.previewing") : t("import.preview")}
          </Button>
          <Button variant="primary" size="sm" icon="upload" aria-label={t("import.import")} disabled={!!busy || !canImport} onClick={() => void run(plans, false)}>
            {busy === "import" ? t("import.importing") : t("import.import")}
          </Button>
        </>
      }
    >
      <input ref={inputRef} type="file" accept="application/json,.json" multiple hidden onChange={(event) => {
        selectFiles(event.target.files);
        event.target.value = "";
      }} />
      <div className="import-modal-panel">
        <p>{t("import.supportedFormats")}</p>
        <button type="button" className="import-modal-dropzone" disabled={!!busy} onClick={() => inputRef.current?.click()}>
          <span className="material-symbols-outlined" aria-hidden>upload_file</span>
          <strong>{t("import.chooseFiles")}</strong>
          <span>{t("import.autoDetect")}</span>
        </button>
        {plans.some(supportsAccountOptions) ? (
          <label className="import-modal-checkbox">
            <input type="checkbox" checked={skipDuplicates} disabled={!!busy} onChange={(event) => {
              setSkipDuplicates(event.target.checked);
              setPlans((rows) => rows.map((row) => supportsAccountOptions(row) && !row.imported ? { ...row, reviewed: false } : row));
            }} />
            {t("import.skipDuplicates")}
          </label>
        ) : null}
        {plans.length ? (
          <div className="import-modal-files" aria-live="polite" aria-busy={!!busy}>
            {plans.map((plan, index) => (
              <article className="import-modal-file" key={`${index}-${plan.file.name}`}>
                <div className="import-modal-file-heading">
                  <strong>{plan.file.name}</strong>
                  <Button variant="ghost" size="sm" icon="close" disabled={!!busy} aria-label={t("import.removeFile", { name: plan.file.name })}
                    onClick={() => setPlans((rows) => rows.filter((_, rowIndex) => rowIndex !== index))} />
                </div>
                {plan.preview ? (
                  <>
                    <p>{SOURCE_LABELS[plan.preview.source]} · {summary(plan.preview) || t("import.noRecords")}</p>
                    {supportsAccountOptions(plan) && plan.preview.total_accounts > 1 ? (
                      <label className="import-modal-count">
                        {t("import.accountCount", { total: plan.preview.total_accounts })}
                        <input type="number" min={1} max={plan.preview.total_accounts} value={plan.importCount} disabled={!!busy || plan.imported}
                          onChange={(event) => {
                            const count = Math.min(plan.preview!.total_accounts, Math.max(1, Number(event.target.value) || 1));
                            setPlans((rows) => rows.map((row, rowIndex) => rowIndex === index ? { ...row, importCount: count, reviewed: false } : row));
                          }} />
                      </label>
                    ) : null}
                    {plan.preview.result.warnings?.map((warning, warningIndex) => <p className="import-modal-warning" key={warningIndex}>{warning}</p>)}
                  </>
                ) : null}
                {plan.error ? <p className="import-modal-error" role="alert">{plan.error}</p> : (
                  <p className="import-modal-status">{plan.imported ? t("import.fileImported") : plan.reviewed ? t("import.fileReady") : busy ? t("import.previewing") : t("import.previewRequired")}</p>
                )}
              </article>
            ))}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}
