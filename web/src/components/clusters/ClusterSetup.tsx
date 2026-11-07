import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, Input, Textarea, Toggle, cn } from "../ui";
import { LendingFields } from "./ClusterPolicies";
import type { ClusterStatus, CreateClusterBody, JoinClusterBody, LendingPolicy, ModelOption } from "./types";
import { DEFAULT_LENDING, cleanLending, isHttpUrl, isPlainHttp, preferredHubUrl } from "./utils";

type Props = {
  status: ClusterStatus;
  modelOptions: ModelOption[];
  busy: string | null;
  onCreate: (body: CreateClusterBody) => Promise<boolean>;
  onJoin: (body: JoinClusterBody) => Promise<boolean>;
};

function InsecureToggle({ checked, onChange, disabled, hint }: { checked: boolean; onChange: (next: boolean) => void; disabled?: boolean; hint: string }) {
  const { t } = useTranslation();
  return (
    <div className={cn("cl-insecure", checked && "is-on")}>
      <Toggle
        className="cl-consent"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        label={
          <span className="cl-consent-copy">
            <strong>{t("clusters.setup.allowInsecure")}</strong>
            <small>{hint}</small>
          </span>
        }
      />
      {checked ? (
        <p className="cl-insecure-warning">
          <span className="material-symbols-outlined" aria-hidden>warning</span>
          {t("clusters.setup.insecureWarning")}
        </p>
      ) : null}
    </div>
  );
}

function CardHead({ icon, title, description }: { icon: string; title: string; description: string }) {
  return (
    <header className="cl-setup-head">
      <span className="material-symbols-outlined cl-setup-icon" aria-hidden>{icon}</span>
      <div>
        <h3>{title}</h3>
        <p>{description}</p>
      </div>
    </header>
  );
}

function CreateCard({ status, modelOptions, busy, onCreate }: Omit<Props, "onJoin">) {
  const { t } = useTranslation();
  const suggestions = status.suggested_hub_urls;
  const [name, setName] = useState("");
  // null until the user types, so a suggestion that arrives with a later poll still fills the field.
  const [hubUrl, setHubUrl] = useState<string | null>(null);
  const [urlTouched, setUrlTouched] = useState(false);
  const [allowInsecure, setAllowInsecure] = useState(false);
  const [lending, setLending] = useState<LendingPolicy>(DEFAULT_LENDING);
  const saving = busy === "create";
  const disabled = busy !== null;

  const url = (hubUrl ?? preferredHubUrl(suggestions)).trim();
  const plainHttp = isPlainHttp(url);
  const blockedByHttp = plainHttp && !allowInsecure;
  const canCreate = Boolean(name.trim()) && isHttpUrl(url) && !blockedByHttp && !disabled;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canCreate) return;
    await onCreate({ name: name.trim(), hub_url: url, allow_insecure: plainHttp && allowInsecure, lending: cleanLending(lending) });
  };

  return (
    <article className="cp-card cl-setup-card">
      <CardHead icon="hub" title={t("clusters.setup.createTitle")} description={t("clusters.setup.createDesc")} />
      <form className="cl-form cl-setup-body" onSubmit={(event) => void submit(event)}>
        <Field label={t("clusters.setup.clusterName")} required>
          <Input value={name} maxLength={80} disabled={disabled} onChange={(event) => setName(event.target.value)} placeholder={t("clusters.setup.clusterNamePlaceholder")} />
        </Field>
        <Field
          label={t("clusters.setup.hubUrl")}
          required
          hint={t("clusters.setup.hubUrlHint")}
          error={urlTouched && url && !isHttpUrl(url) ? t("clusters.setup.hubUrlInvalid") : undefined}
        >
          <Input
            type="url"
            value={hubUrl ?? url}
            disabled={disabled}
            spellCheck={false}
            autoComplete="off"
            onChange={(event) => setHubUrl(event.target.value)}
            onBlur={() => setUrlTouched(true)}
            placeholder="https://tproxy.example.com"
          />
        </Field>
        {suggestions.length ? (
          <div className="cl-suggest">
            <span>{t("clusters.setup.suggested")}</span>
            {suggestions.map((suggestion) => (
              <button
                key={suggestion}
                type="button"
                className={cn("cl-chip", suggestion === url && "is-on")}
                aria-pressed={suggestion === url}
                disabled={disabled}
                onClick={() => setHubUrl(suggestion)}
              >
                <span className="material-symbols-outlined" aria-hidden>{/^https:/i.test(suggestion) ? "lock" : "lan"}</span>
                <span>{suggestion}</span>
              </button>
            ))}
          </div>
        ) : null}
        {plainHttp ? (
          <InsecureToggle
            checked={allowInsecure}
            onChange={setAllowInsecure}
            disabled={disabled}
            hint={allowInsecure ? t("clusters.setup.allowInsecureHint") : t("clusters.setup.allowInsecureRequired")}
          />
        ) : null}
        <LendingFields compact value={lending} onChange={setLending} modelOptions={modelOptions} disabled={disabled} />
        <div className="cl-setup-actions">
          <Button type="submit" icon="hub" loading={saving} disabled={!canCreate}>{t("clusters.setup.create")}</Button>
        </div>
      </form>
    </article>
  );
}

function JoinCard({ status, modelOptions, busy, onJoin }: Omit<Props, "onCreate">) {
  const { t } = useTranslation();
  const [token, setToken] = useState("");
  // null = untouched: follow this node's current name.
  const [nodeName, setNodeName] = useState<string | null>(null);
  const [owner, setOwner] = useState("");
  const [allowInsecure, setAllowInsecure] = useState(false);
  const [lending, setLending] = useState<LendingPolicy>(DEFAULT_LENDING);
  const saving = busy === "join";
  const disabled = busy !== null;
  const name = (nodeName ?? status.node.name).trim();
  const canJoin = Boolean(token.trim()) && Boolean(name) && !disabled;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canJoin) return;
    await onJoin({ token: token.trim(), node_name: name, owner: owner.trim(), allow_insecure: allowInsecure, lending: cleanLending(lending) });
  };

  return (
    <article className="cp-card cl-setup-card">
      <CardHead icon="group_add" title={t("clusters.setup.joinTitle")} description={t("clusters.setup.joinDesc")} />
      <form className="cl-form cl-setup-body" onSubmit={(event) => void submit(event)}>
        <Field label={t("clusters.setup.token")} required hint={t("clusters.setup.tokenHint")}>
          <Textarea
            className="cl-token-input"
            rows={3}
            value={token}
            disabled={disabled}
            spellCheck={false}
            autoComplete="off"
            onChange={(event) => setToken(event.target.value)}
            placeholder={t("clusters.setup.tokenPlaceholder")}
          />
        </Field>
        <div className="cl-field-grid">
          <Field label={t("clusters.setup.nodeName")} required>
            <Input
              value={nodeName ?? status.node.name}
              maxLength={80}
              disabled={disabled}
              onChange={(event) => setNodeName(event.target.value)}
              placeholder={t("clusters.setup.nodeNamePlaceholder")}
            />
          </Field>
          <Field label={t("clusters.setup.owner")} hint={t("clusters.setup.ownerHint")}>
            <Input value={owner} maxLength={80} disabled={disabled} onChange={(event) => setOwner(event.target.value)} placeholder={t("clusters.setup.ownerPlaceholder")} />
          </Field>
        </div>
        <InsecureToggle checked={allowInsecure} onChange={setAllowInsecure} disabled={disabled} hint={t("clusters.setup.allowInsecureJoinHint")} />
        <LendingFields compact value={lending} onChange={setLending} modelOptions={modelOptions} disabled={disabled} />
        <div className="cl-setup-actions">
          <Button type="submit" icon="login" loading={saving} disabled={!canJoin}>{t("clusters.setup.join")}</Button>
        </div>
      </form>
    </article>
  );
}

/** Standalone node: become the hub of a new cluster, or join an existing one. */
export function ClusterSetup(props: Props) {
  const { onCreate, onJoin, ...shared } = props;
  return (
    <div className="cl-setup">
      <CreateCard {...shared} onCreate={onCreate} />
      <JoinCard {...shared} onJoin={onJoin} />
    </div>
  );
}
