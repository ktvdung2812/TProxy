import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { Button, Card, Input } from "../ui";

type Metric = {provider_id:string; model_id:string; credential_id:string; requests:number; errors:number; fallbacks:number; p95_ms:number; ttft_p95_ms:number|null; queue_ms:number; input_tokens:number; output_tokens:number; cached_tokens:number; cache_creation_tokens:number};
type Account = {credential_id:string; in_flight:number; waiting:number; samples:number; latency_ms:number; ttft_ms:number; error_rate:number; quota_remaining?:number; quota_at:string};
type Alert = {id:string; kind:string; resource_id:string; first_seen:string};
type Snapshot = {metrics:Metric[];accounts:Account[];alerts:Alert[];since:string;evaluated_at:string};

export function OperationsView({secret}:{secret:string}) {
  const {t}=useTranslation();
  const [data,setData]=useState<Snapshot>();
  const [error,setError]=useState("");
  const [query,setQuery]=useState("");
  const [refresh,setRefresh]=useState(0);
  useEffect(()=>{
    const controller=new AbortController(); let timer:ReturnType<typeof setTimeout>;
    const load=async()=>{
      try {
        const response=await fetch("/api/admin/operations",{headers:secret?{Authorization:`Bearer ${secret}`}:{},signal:controller.signal});
        const body=await response.json();if(!response.ok)throw new Error(body?.error?.message||`HTTP ${response.status}`);
        if(!controller.signal.aborted){setData(body);setError("");}
      }catch(cause){if(!controller.signal.aborted)setError(String(cause));}
      finally{if(!controller.signal.aborted)timer=setTimeout(load,15000);}
    };
    void load();return()=>{controller.abort();clearTimeout(timer);};
  },[secret,refresh]);
  const rows=useMemo(()=>data?.metrics.filter(row=>`${row.provider_id} ${row.model_id} ${row.credential_id}`.toLowerCase().includes(query.toLowerCase()))??[],[data,query]);
  const number=(value:number)=>value.toLocaleString(undefined,{maximumFractionDigits:1});
  return <section className="section" style={{display:"grid",gap:16}}>
    <Card title={t("operations.title")} subtitle={t("operations.hint")} action={<Button icon="refresh" onClick={()=>setRefresh(v=>v+1)}>{t("common.refresh")}</Button>}>
      {error&&<p role="alert" className="banner banner-error">{error}</p>}
      {!data&&!error&&<p>{t("common.loading")}</p>}
      {data&&<>
        <p className="muted">{t("operations.updated")}: {new Date(data.evaluated_at).toLocaleTimeString()}</p>
        <div aria-live="polite">
          {data.alerts.length===0?<p>{t("operations.noAlerts")}</p>:data.alerts.map(alert=><div className="banner banner-error" key={alert.id} style={{marginBottom:8}}>
            <span><strong>{t(`operations.alerts.${alert.kind}`)}</strong> · {alert.resource_id} · {new Date(alert.first_seen).toLocaleTimeString()}</span>
            <Link to={alert.kind==="quota_exhausted"?"/quota":alert.kind==="route_unavailable"?"/models":"/providers"}>{t("operations.inspect")}</Link>
          </div>)}
        </div>
      </>}
    </Card>
    <Card title={t("operations.requests")} subtitle={t("operations.tokenHint")}>
      <Input aria-label={t("operations.filter")} placeholder={t("operations.filter")} value={query} onChange={e=>setQuery(e.target.value)}/>
      <div style={{overflowX:"auto",marginTop:12}}><table className="data-table" style={{width:"100%"}}>
        <thead><tr>{["account","attempts","errors","fallbacks","latency","ttft","queue","input","output","cacheRead","cacheWrite"].map(key=><th key={key}>{t(`operations.${key}`)}</th>)}</tr></thead>
        <tbody>{rows.map(row=><tr key={`${row.provider_id}/${row.model_id}/${row.credential_id}`}>
          <td><strong>{row.model_id}</strong><br/>{row.provider_id} / {row.credential_id}</td><td>{row.requests}</td><td>{number(100*row.errors/row.requests)}%</td><td>{row.fallbacks}</td><td>{number(row.p95_ms)} ms</td><td>{row.ttft_p95_ms===null?"—":`${number(row.ttft_p95_ms)} ms`}</td><td>{number(row.queue_ms)} ms</td><td>{number(row.input_tokens)}</td><td>{number(row.output_tokens)}</td><td>{number(row.cached_tokens)}</td><td>{number(row.cache_creation_tokens)}</td>
        </tr>)}</tbody>
      </table>{data&&rows.length===0&&<p>{t("operations.noRequests")}</p>}</div>
    </Card>
    <Card title={t("operations.liveAccounts")} subtitle={t("operations.runtimeHint")}>
      <div style={{overflowX:"auto"}}><table className="data-table" style={{width:"100%"}}>
        <thead><tr>{["account","inFlight","waiting","samples","ewma","errors","quota"].map(key=><th key={key}>{t(`operations.${key}`)}</th>)}</tr></thead>
        <tbody>{data?.accounts.filter(a=>a.credential_id.toLowerCase().includes(query.toLowerCase())).map(account=><tr key={account.credential_id}>
          <td>{account.credential_id}</td><td>{account.in_flight}</td><td>{account.waiting}</td><td>{account.samples}</td><td>{number(account.latency_ms)} ms</td><td>{number(account.error_rate*100)}%</td><td>{account.quota_remaining===undefined?"—":`${number(account.quota_remaining*100)}%${Date.now()-Date.parse(account.quota_at)>300000?` (${t("operations.stale")})`:""}`}</td>
        </tr>)}</tbody>
      </table></div>
    </Card>
  </section>;
}
