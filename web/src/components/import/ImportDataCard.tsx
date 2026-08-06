import { useTranslation } from "react-i18next";
import { Button, Card } from "../ui";

type Props = {
  onOpen: () => void;
};

export function ImportDataCard({ onOpen }: Props) {
  const { t } = useTranslation();

  return (
    <Card className="import-data-card" pad="md">
      <div className="import-data-card-inner">
        <div>
          <h3>{t("import.cardTitle")}</h3>
          <p>
            {t("import.cardDesc")}
          </p>
        </div>
        <Button variant="primary" size="sm" icon="upload" onClick={onOpen}>
          {t("import.cardButton")}
        </Button>
      </div>
    </Card>
  );
}
