import { useTranslation } from "react-i18next";
import { Modal } from "../ui";
import { CopyAction } from "./CopyBits";

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

/** Every file the tool needs, ready to paste on the machine where it runs. */
export function ManualConfigModal({ open, onClose, title, configs }: Props) {
  const { t } = useTranslation();

  return (
    <Modal open={open} onClose={onClose} title={title || t("cliTools.manualConfig")} size="lg" icon="content_copy">
      <div className="ct-manual">
        <p className="ct-hint">{t("cliTools.manualModal.hint", { count: configs.length })}</p>
        {configs.map((config, index) => (
          <div key={`${config.filename}-${index}`} className="ct-code">
            <div className="ct-code-head">
              <span className="ct-code-file">
                <span className="material-symbols-outlined" aria-hidden>description</span>
                {config.filename}
              </span>
              <CopyAction text={config.content} label={t("cliTools.manualModal.copyFile", { file: config.filename })} bordered={false} />
            </div>
            <pre>
              <code>{config.content}</code>
            </pre>
          </div>
        ))}
      </div>
    </Modal>
  );
}
