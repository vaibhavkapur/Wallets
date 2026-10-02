import { useState } from "react";
import type { Receipt } from "@asa/sdk";
import { useClient } from "../session";
import { Card, ErrorBox, Status, Table, errorMessage, useAsync } from "../components/ui";
import { fmtAmount, fmtTime, shortId } from "../format";

export function Tasks() {
  const client = useClient();
  const tasks = useAsync(() => client.listTasks({ limit: 200 }), [client]);
  const receipts = useAsync(() => client.listReceipts({ limit: 500 }), [client]);
  const [err, setErr] = useState<string | null>(null);

  const costByTask = new Map<string, bigint>();
  for (const r of receipts.data ?? []) {
    costByTask.set(r.task_id, (costByTask.get(r.task_id) ?? 0n) + BigInt(r.settled_amount));
  }

  async function close(id: string) {
    if (!confirm("Close this task? The agent can no longer spend on it.")) return;
    try {
      await client.closeTask(id);
      tasks.reload();
    } catch (e) {
      setErr(errorMessage(e));
    }
  }

  return (
    <Card title="Tasks (cost per task)" actions={<button onClick={() => { tasks.reload(); receipts.reload(); }}>Refresh</button>}>
      <ErrorBox error={tasks.error ?? err} />
      <Table
        head={["Task", "Agent", "Description", "Status", "Budget", "Spent (settled)", "Deadline", ""]}
        rows={(tasks.data ?? []).map((t) => [
          <code key="i">{shortId(t.id, 14)}</code>,
          <code key="a">{shortId(t.agent_id, 14)}</code>,
          t.description || <em>—</em>,
          <Status key="s" value={t.status} />,
          fmtAmount(t.budget),
          fmtAmount((costByTask.get(t.id) ?? 0n).toString()),
          fmtTime(t.deadline),
          t.status === "active" ? (
            <button key="c" onClick={() => close(t.id)}>
              Close
            </button>
          ) : null,
        ])}
      />
    </Card>
  );
}

export function Transactions() {
  const client = useClient();
  const attempts = useAsync(() => client.listPaymentAttempts({ limit: 200 }), [client]);
  const receipts = useAsync(() => client.listReceipts({ limit: 500 }), [client]);
  const byAttempt = new Map<string, Receipt>((receipts.data ?? []).map((r) => [r.payment_attempt_id, r]));

  return (
    <>
      <Card title="Payment attempts" actions={<button onClick={() => { attempts.reload(); receipts.reload(); }}>Refresh</button>}>
        <ErrorBox error={attempts.error} />
        <Table
          head={["When", "Status", "Resource", "Beneficiary", "Quoted", "Settled", "Service", "Tx / reason"]}
          rows={(attempts.data ?? []).map((a) => {
            const r = byAttempt.get(a.id);
            return [
              fmtTime(a.created_at),
              <Status key="s" value={a.status} />,
              <span key="u" title={a.url}>
                {a.method} {shortUrl(a.url)}
              </span>,
              a.quote ? <code key="p">{shortId(a.quote.pay_to, 12)}</code> : "—",
              a.quote ? fmtAmount(a.quote.max_amount_required) : "—",
              a.status === "settled" ? fmtAmount(a.settled_amount) : "—",
              r?.service ? (
                <span key="d" className={r.service.delivered ? "ok" : "error"}>
                  {r.service.delivered ? "delivered" : `not delivered (${r.service.http_status}${r.service.truncated ? ", truncated" : ""})`}
                </span>
              ) : (
                "—"
              ),
              a.submission_ref ? <code key="t">{shortId(a.submission_ref, 14)}</code> : <span key="t" className="muted">{a.reject_reason || a.error || ""}</span>,
            ];
          })}
        />
        <p className="muted">
          Settlement and service delivery are tracked separately. A settled payment whose result was not delivered is a support/dispute case, not a reason to pay again.
        </p>
      </Card>
    </>
  );
}

export function Ledger() {
  const client = useClient();
  const entries = useAsync(() => client.listLedger(300), [client]);
  const audit = useAsync(() => client.listAudit(300), [client]);
  return (
    <>
      <Card title="Ledger (double-entry)" actions={<button onClick={entries.reload}>Refresh</button>}>
        <ErrorBox error={entries.error} />
        <Table
          head={["When", "Journal", "Account", "Debit", "Credit", "Memo"]}
          rows={(entries.data ?? []).map((e) => [
            fmtTime(e.created_at),
            <code key="j">{shortId(e.journal_id, 12)}</code>,
            <code key="a">{e.account}</code>,
            e.direction === "debit" ? fmtAmount(e.amount) : "",
            e.direction === "credit" ? fmtAmount(e.amount) : "",
            e.memo,
          ])}
        />
      </Card>
      <Card title="Audit trail" actions={<button onClick={audit.reload}>Refresh</button>}>
        <ErrorBox error={audit.error} />
        <Table
          head={["When", "Actor", "Action", "Subject", "Details"]}
          rows={(audit.data ?? []).map((e) => [
            fmtTime(e.created_at),
            <code key="a">{shortId(e.actor, 18)}</code>,
            e.action,
            <code key="s">
              {e.subject_type} {shortId(e.subject_id, 12)}
            </code>,
            <code key="d" className="details">
              {e.details ? JSON.stringify(e.details) : ""}
            </code>,
          ])}
        />
      </Card>
    </>
  );
}

function shortUrl(u: string): string {
  try {
    const x = new URL(u);
    return `${x.host}${x.pathname}${x.search ? "?" + x.search.slice(1, 24) : ""}`;
  } catch {
    return u;
  }
}
