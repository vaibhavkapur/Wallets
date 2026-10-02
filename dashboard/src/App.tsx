import { useState, type FormEvent } from "react";
import { SessionProvider, useSession } from "./session";
import { Overview } from "./pages/Overview";
import { Agents } from "./pages/Agents";
import { Ledger, Tasks, Transactions } from "./pages/Activity";

type Page = "overview" | "agents" | "tasks" | "transactions" | "ledger";

const PAGES: { id: Page; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "agents", label: "Agents & budgets" },
  { id: "tasks", label: "Tasks" },
  { id: "transactions", label: "Transactions" },
  { id: "ledger", label: "Ledger & audit" },
];

export function App() {
  return (
    <SessionProvider>
      <Shell />
    </SessionProvider>
  );
}

function Shell() {
  const { session, signOut } = useSession();
  const [page, setPage] = useState<Page>("overview");
  if (!session) return <SignIn />;
  return (
    <div className="layout">
      <nav className="sidebar">
        <h1>Agent Spending Accounts</h1>
        <p className="muted small">Owner console</p>
        {PAGES.map((p) => (
          <button key={p.id} className={page === p.id ? "nav active" : "nav"} onClick={() => setPage(p.id)}>
            {p.label}
          </button>
        ))}
        <div className="spacer" />
        <p className="muted small">{session.controlPlaneUrl}</p>
        <button onClick={signOut}>Sign out</button>
      </nav>
      <main>
        {page === "overview" && <Overview />}
        {page === "agents" && <Agents />}
        {page === "tasks" && <Tasks />}
        {page === "transactions" && <Transactions />}
        {page === "ledger" && <Ledger />}
      </main>
    </div>
  );
}

function SignIn() {
  const { signIn } = useSession();
  const [url, setUrl] = useState(import.meta.env.VITE_CONTROL_PLANE_URL ?? "http://127.0.0.1:8080");
  const [key, setKey] = useState("");
  const [err, setErr] = useState<string | null>(null);
  function submit(e: FormEvent) {
    e.preventDefault();
    if (!key.startsWith("owner_")) {
      setErr("Use an owner API key (owner_…). Agent keys cannot administer an account.");
      return;
    }
    signIn({ controlPlaneUrl: url.trim(), apiKey: key.trim() });
  }
  return (
    <div className="signin">
      <form onSubmit={submit} className="card">
        <h1>Agent Spending Accounts</h1>
        <p className="muted">Sign in with an owner API key. The key is kept in this tab's session only.</p>
        <label>
          Control plane URL
          <input value={url} onChange={(e) => setUrl(e.target.value)} />
        </label>
        <label>
          Owner API key
          <input value={key} onChange={(e) => setKey(e.target.value)} type="password" autoComplete="off" />
        </label>
        {err && <p className="error">{err}</p>}
        <button className="primary" type="submit">
          Open console
        </button>
      </form>
    </div>
  );
}
