import { useTranslation } from "react-i18next";
import { Tag } from "../page-kit";

/** "Cluster" tag for read-only items a member received from the cluster hub. */
export function ManagedTag() {
  const { t } = useTranslation();
  return (
    <Tag tone="info" title={t("clusters.managed.hint")}>
      {t("clusters.managed.tag")}
    </Tag>
  );
}
