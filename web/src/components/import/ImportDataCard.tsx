import { Button, Card } from "../ui";

type Props = {
  onOpen: () => void;
};

export function ImportDataCard({ onOpen }: Props) {
  return (
    <Card className="import-data-card" pad="md">
      <div className="import-data-card-inner">
        <div>
          <h3>Import data</h3>
          <p>
            Migrate from <code>9router-backup-*.json</code> or CLIProxyAPI auth files such as{" "}
            <code>codex-*.json</code>.
          </p>
        </div>
        <Button variant="primary" size="sm" icon="upload" onClick={onOpen}>
          Import…
        </Button>
      </div>
    </Card>
  );
}
