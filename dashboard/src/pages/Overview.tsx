import { useState, type FormEvent } from "react";
import { useClient } from "../session";
import { Card, ErrorBox, Status, Table, errorMessage, useAsync } from "../components/ui";
import { fmtAmount, fmtTime, toBaseUnits } from "../format";

const DEFAULT_ASSET = "0x036CbD53842c5426634e7929541eC2318f3dCF7e";
const DEFAULT_NETWORK = "base-sepolia";

export function Overview() {
  const client = useClient();
  const org = useAsync(() => client.getOrganization(), [client]);
  const balances = useAsync(() => client.listBalances(), [client]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function toggle() {
    if (!org.data) return;
    const paused = org.data.status === "paused";
    if (!paused && !confirm("Pause all spending for this organization? In-flight payments remain tracked until resolved.")) return;
    setBusy(true);
    try {
      paused ? await client.resume() : await client.pause();
      org.reload();
    } catch (e) {
      setErr(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Card
        title="Organization"
        actions={
          org.data && (
            <button onClick={toggle} disabled={busy} className={org.data.status === "paused" ? "primary" : "danger"}>
              {org.data.status === "paused" ? "Resume spending" : "Pause all spending"}
            </button>
          )
        }
      >
        <ErrorBox error={org.error ?? err} />
        {org.data && (
          <dl className="kv">
            <dt>Name</dt>
            <dd>{org.data.name}</dd>
            <dt>Status</dt>
            <dd>
              <Status value={org.data.status} />
            </dd>
            <dt>ID</dt>
            <dd>
              <code>{org.data.id}</code>
            </dd>
          </dl>
        )}
      </Card>

      <Card title="Balances" actions={<button onClick={balances.reload}>Refresh</button>}>
        <ErrorBox error={balances.error} />
        <Table
          head={["Asset", "Network", "Available", "Reserved (open holds)", "Updated"]}
          rows={(balances.data ?? []).map((b) => [<code key="a">{b.asset}</code>, b.network, fmtAmount(b.available), fmtAmount(b.reserved), fmtTime(b.updated_at)])}
          empty="No funds yet. Deposit below."
        />
        <FundingForm onDone={balances.reload} />
      </Card>
    </>
  );
}

function FundingForm({ onDone }: { onDone: () => void }) {
  const client = useClient();
  const [mode, setMode] = useState<"deposit" | "withdraw">("deposit");
  const [asset, setAsset] = useState(DEFAULT_ASSET);
  const [network, setNetwork] = useState(DEFAULT_NETWORK);
  const [value, setValue] = useState("");
  const [reference, setReference] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setOk(null);
    const parsed = toBaseUnits(value, 6);
    if (!parsed.amount) {
      setErr(parsed.error ?? "invalid amount");
      return;
    }
    try {
      const input = { asset, network, amount: parsed.amount, reference };
      const b = mode === "deposit" ? await client.deposit(input) : await client.withdraw(input);
      setOk(`${mode} recorded; available ${fmtAmount(b.available)}`);
      setValue("");
      onDone();
    } catch (e2) {
      setErr(errorMessage(e2));
    }
  }

  return (
    <form className="inline-form" onSubmit={submit}>
      <select value={mode} onChange={(e) => setMode(e.target.value as "deposit" | "withdraw")}>
        <option value="deposit">Deposit</option>
        <option value="withdraw">Withdraw</option>
      </select>
      <input value={asset} onChange={(e) => setAsset(e.target.value)} placeholder="asset (contract)" size={44} />
      <input value={network} onChange={(e) => setNetwork(e.target.value)} placeholder="network" size={12} />
      <input value={value} onChange={(e) => setValue(e.target.value)} placeholder="amount, e.g. 25.00" size={14} required />
      <input value={reference} onChange={(e) => setReference(e.target.value)} placeholder="reference" size={16} />
      <button type="submit" className="primary">
        {mode === "deposit" ? "Record deposit" : "Withdraw"}
      </button>
      <ErrorBox error={err} />
      {ok && <p className="ok">{ok}</p>}
      <p className="muted">Owner-controlled funding. Reserved funds cannot be withdrawn; agents can never deposit, withdraw, or enable top-ups.</p>
    </form>
  );
}
