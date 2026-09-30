import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, Field, Input, Select, Textarea } from "../ui";

const examples:Record<string,unknown>={
  openai:{model:"diagnostic-model",messages:[{role:"user",content:"Hello"}],stream:true},
  responses:{model:"diagnostic-model",instructions:"Be concise",input:[{role:"user",content:"Hello"}],stream:true},
  claude:{model:"diagnostic-model",system:"Be concise",max_tokens:512,messages:[{role:"user",content:"Hello"}]},
  gemini:{contents:[{role:"user",parts:[{text:"Hello"}]}],generationConfig:{maxOutputTokens:512}},
};

export function DiagnosticsView({secret}:{secret:string}) {
  const {t}=useTranslation();const [source,setSource]=useState("openai");const [target,setTarget]=useState("codex");
  const [model,setModel]=useState("diagnostic-model");const [body,setBody]=useState(JSON.stringify(examples.openai,null,2));
  const [result,setResult]=useState<{canonical:unknown;outbound:unknown}>();const [error,setError]=useState("");const [busy,setBusy]=useState(false);
  const preview=async()=>{
    setBusy(true);setError("");setResult(undefined);
    try{
      const response=await fetch("/api/admin/diagnostics/translate",{method:"POST",headers:{"Content-Type":"application/json",...(secret?{Authorization:`Bearer ${secret}`}:{})},body:JSON.stringify({source,target,model,body:JSON.parse(body)})});
      const data=await response.json();if(!response.ok)throw new Error(data?.error?.message||`HTTP ${response.status}`);setResult(data);
    }catch(cause){setError(String(cause));}finally{setBusy(false);}
  };
  return <section className="section" style={{display:"grid",gap:16}}>
    <Card title={t("diagnostics.title")} subtitle={t("diagnostics.hint")}>
      <form onSubmit={e=>{e.preventDefault();void preview();}} style={{display:"grid",gap:12}}>
        <div className="inline-fields">
          <Field label={t("diagnostics.source")}><Select aria-label={t("diagnostics.source")} value={source} onChange={e=>{setSource(e.target.value);setBody(JSON.stringify(examples[e.target.value],null,2));setResult(undefined);}}>{["openai","responses","claude","gemini"].map(value=><option key={value}>{value}</option>)}</Select></Field>
          <Field label={t("diagnostics.target")}><Select aria-label={t("diagnostics.target")} value={target} onChange={e=>setTarget(e.target.value)}>{["openai","codex","claude","gemini"].map(value=><option key={value}>{value}</option>)}</Select></Field>
          <Field label={t("diagnostics.model")}><Input aria-label={t("diagnostics.model")} required value={model} onChange={e=>setModel(e.target.value)}/></Field>
        </div>
        <Field label={t("diagnostics.input")}><Textarea aria-label={t("diagnostics.input")} rows={14} value={body} onChange={e=>setBody(e.target.value)} spellCheck={false} style={{fontFamily:"monospace"}}/></Field>
        <Button type="submit" icon="transform" loading={busy} disabled={busy}>{t("diagnostics.preview")}</Button>
        {error&&<p role="alert" className="banner banner-error">{error}</p>}
      </form>
    </Card>
    {result&&<div style={{display:"grid",gridTemplateColumns:"repeat(auto-fit,minmax(min(100%,360px),1fr))",gap:16}}>{(["canonical","outbound"] as const).map(key=><Card key={key} title={t(`diagnostics.${key}`)} action={<Button size="sm" icon="content_copy" onClick={()=>void navigator.clipboard.writeText(JSON.stringify(result[key],null,2)).catch(cause=>setError(String(cause)))}>{t("diagnostics.copy")}</Button>}><pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere",fontSize:12}}>{JSON.stringify(result[key],null,2)}</pre></Card>)}</div>}
  </section>;
}
