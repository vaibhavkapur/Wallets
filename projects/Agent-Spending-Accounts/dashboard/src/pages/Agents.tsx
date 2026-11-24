import { useState, type FormEvent } from "react";
import type { AgentPrincipal, BudgetPolicyVersion, EndpointRule, PolicyInput } from "@asa/sdk";
import { useClient } from "../session";
import { Card, ErrorBox, Secret, Status, Table, errorMessage, useAsync } from "../components/ui";
import { fmtAmount, fmtTime, toBaseUnits } from "../format";

export function Agents() {
  const client = useClient();
  const agents = useAsync(() => client.listAgents(), [client]);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<{ agent: AgentPrincipal; agent_api_key: string } | null>(null);
  const [selected, setSelected] = useState<AgentPrincipal | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function create(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    try {
      const r = await client.createAgent(name.trim());
      setCreated(r);
      setName("");
      agents.reload();
      setSelected(r.agent);
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  }

  async function revoke(a: AgentPrincipal) {
    if (!confirm(`Revoke ${a.name}? It can no longer start payments. Submitted payments stay tracked until resolved.`)) return;
    try {
      await client.revokeAgent(a.id);
      agents.reload();
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  }

  return (
    <>
      <Card title="Agents">
        <ErrorBox error={agents.error ?? err} />
        {created && <Secret label={`API key for ${created.agent.name}`} value={created.agent_api_key} />}
        <Table
          head={["Name", "Status", "Policy", "Created", ""]}
          rows={(agents.data ?? []).map((a) => [
            <button key="n" className="link" onClick={() => setSelected(a)}>
              {a.name}
            </button>,
            <Status key="s" value={a.status} />,
            a.policy_version_id ? <code key="p">{a.policy_version_id}</code> : <em key="p">none — cannot spend</em>,
            fmtTime(a.created_at),
            a.status === "active" ? (
              <button key="r" className="danger" onClick={() => revoke(a)}>
                Revoke
              </button>
            ) : (
              <span key="r" className="muted">
                revoked {fmtTime(a.revoked_at)}
              </span>
            ),
          ])}
        />
        <form className="inline-form" onSubmit={create}>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="new agent name" required />
          <button className="primary" type="submit">
            Create agent
          </button>
        </form>
      </Card>
      {selected && <PolicyEditor agent={selected} onSaved={agents.reload} />}
    </>
  );
}

interface Draft {
  asset: string;
  network: string;
  decimals: string;
  scheme: string;
  perRequest: string;
  perTask: string;
  agentDaily: string;
  orgDaily: string;
  fee: string;
  maxTaskSeconds: string;
  grantTtl: string;
  endpoints: { url_prefix: string; methods: string; recipients: string }[];
}

function fromPolicy(p: BudgetPolicyVersion | undefined): Draft {
  const d = p?.asset.decimals ?? 6;
  const f = (a: string | undefined) => (a === undefined ? "" : fmtAmount(a, d, "").trim());
  return {
    asset: p?.asset.asset ?? "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    network: p?.asset.network ?? "base-sepolia",
    decimals: String(d),
    scheme: p?.asset.scheme ?? "exact",
    perRequest: f(p?.per_request_max) || "0.50",
    perTask: f(p?.per_task_max) || "5",
    agentDaily: f(p?.agent_daily_max) || "20",
    orgDaily: f(p?.org_daily_max) || "100",
    fee: f(p?.fee_allowance) || "0",
    maxTaskSeconds: String(p?.max_task_seconds ?? 86400),
    grantTtl: String(p?.grant_ttl_seconds ?? 60),
    endpoints: (p?.endpoints ?? [{ url_prefix: "", methods: ["GET"], recipients: [] }]).map((e) => ({
      url_prefix: e.url_prefix,
      methods: (e.methods ?? ["GET"]).join(","),
      recipients: e.recipients.join("\n"),
    })),
  };
}

function PolicyEditor({ agent, onSaved }: { agent: AgentPrincipal; onSaved: () => void }) {
  const client = useClient();
  const current = useAsync(async () => (agent.policy_version_id ? client.getPolicy(agent.id) : undefined), [client, agent.id, agent.policy_version_id]);
  const versions = useAsync(() => client.listPolicyVersions(agent.id), [client, agent.id]);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const d = draft ?? fromPolicy(current.data);

  function set<K extends keyof Draft>(k: K, v: Draft[K]) {
    setDraft({ ...d, [k]: v });
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setSaved(null);
    const decimals = Number(d.decimals);
    const conv = (label: string, text: string) => {
      const r = toBaseUnits(text, decimals);
      if (!r.amount) throw new Error(`${label}: ${r.error}`);
      return r.amount;
    };
    try {
      const endpoints: EndpointRule[] = d.endpoints
        .filter((ep) => ep.url_prefix.trim())
        .map((ep) => ({
          url_prefix: ep.url_prefix.trim(),
          methods: ep.methods.split(",").map((m) => m.trim().toUpperCase()).filter(Boolean),
          recipients: ep.recipients.split(/[\n,]/).map((r) => r.trim()).filter(Boolean),
        }));
      const policy: PolicyInput = {
        asset: { asset: d.asset.trim(), network: d.network.trim(), decimals, scheme: d.scheme.trim() },
        per_request_max: conv("per request", d.perRequest),
        per_task_max: conv("per task", d.perTask),
        agent_daily_max: conv("agent daily", d.agentDaily),
        org_daily_max: conv("organization daily", d.orgDaily),
        fee_allowance: conv("fee allowance", d.fee),
        endpoints,
        max_task_seconds: Number(d.maxTaskSeconds),
        grant_ttl_seconds: Number(d.grantTtl),
      };
      const v = await client.setPolicy(agent.id, policy);
      setSaved(`Saved policy version ${v.version}. Tasks created under earlier versions can no longer spend.`);
      setDraft(null);
      current.reload();
      versions.reload();
      onSaved();
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  }

  return (
    <Card title={`Budget policy — ${agent.name}`}>
      <ErrorBox error={err ?? current.error} />
      {saved && <p className="ok">{saved}</p>}
      <form onSubmit={save} className="grid-form">
        <fieldset>
          <legend>Asset (one stablecoin, one network, one scheme)</legend>
          <label>
            Asset contract <input value={d.asset} onChange={(e) => set("asset", e.target.value)} size={44} />
          </label>
          <label>
            Network <input value={d.network} onChange={(e) => set("network", e.target.value)} />
          </label>
          <label>
            Decimals <input value={d.decimals} onChange={(e) => set("decimals", e.target.value)} size={3} />
          </label>
          <label>
            Scheme <input value={d.scheme} onChange={(e) => set("scheme", e.target.value)} size={8} />
          </label>
        </fieldset>
        <fieldset>
          <legend>Limits (in whole units; stored as exact base units)</legend>
          <label>
            Per request <input value={d.perRequest} onChange={(e) => set("perRequest", e.target.value)} />
          </label>
          <label>
            Per task <input value={d.perTask} onChange={(e) => set("perTask", e.target.value)} />
          </label>
          <label>
            Agent per day <input value={d.agentDaily} onChange={(e) => set("agentDaily", e.target.value)} />
          </label>
          <label>
            Organization per day <input value={d.orgDaily} onChange={(e) => set("orgDaily", e.target.value)} />
          </label>
          <label>
            Fee allowance per request <input value={d.fee} onChange={(e) => set("fee", e.target.value)} />
          </label>
          <label>
            Max task seconds <input value={d.maxTaskSeconds} onChange={(e) => set("maxTaskSeconds", e.target.value)} />
          </label>
          <label>
            Grant TTL seconds (≤300) <input value={d.grantTtl} onChange={(e) => set("grantTtl", e.target.value)} />
          </label>
        </fieldset>
        <fieldset>
          <legend>Approved endpoints and beneficiaries (agents cannot add these)</legend>
          {d.endpoints.map((ep, i) => (
            <div className="endpoint" key={i}>
              <input
                value={ep.url_prefix}
                placeholder="https://api.provider.example/v1"
                size={44}
                onChange={(e) => set("endpoints", d.endpoints.map((x, j) => (j === i ? { ...x, url_prefix: e.target.value } : x)))}
              />
              <input value={ep.methods} placeholder="GET,POST" size={10} onChange={(e) => set("endpoints", d.endpoints.map((x, j) => (j === i ? { ...x, methods: e.target.value } : x)))} />
              <textarea
                value={ep.recipients}
                placeholder={"approved payTo addresses, one per line"}
                rows={2}
                onChange={(e) => set("endpoints", d.endpoints.map((x, j) => (j === i ? { ...x, recipients: e.target.value } : x)))}
              />
              <button type="button" onClick={() => set("endpoints", d.endpoints.filter((_, j) => j !== i))}>
                Remove
              </button>
            </div>
          ))}
          <button type="button" onClick={() => set("endpoints", [...d.endpoints, { url_prefix: "", methods: "GET", recipients: "" }])}>
            Add endpoint
          </button>
        </fieldset>
        <button className="primary" type="submit">
          Save as new policy version
        </button>
      </form>
      <h3>Version history</h3>
      <Table
        head={["Version", "Per request", "Per task", "Agent/day", "Org/day", "Endpoints", "Created"]}
        rows={(versions.data ?? []).map((v) => [
          v.version,
          fmtAmount(v.per_request_max, v.asset.decimals),
          fmtAmount(v.per_task_max, v.asset.decimals),
          fmtAmount(v.agent_daily_max, v.asset.decimals),
          fmtAmount(v.org_daily_max, v.asset.decimals),
          v.endpoints.map((e) => e.url_prefix).join(", "),
          fmtTime(v.created_at),
        ])}
      />
    </Card>
  );
}
