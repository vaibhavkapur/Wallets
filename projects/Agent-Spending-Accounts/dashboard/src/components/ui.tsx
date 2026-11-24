import { useCallback, useEffect, useState, type ReactNode } from "react";
import { AsaError } from "@asa/sdk";

export function errorMessage(e: unknown): string {
  if (e instanceof AsaError) return `${e.code}: ${e.message}`;
  return e instanceof Error ? e.message : String(e);
}

/** Small async data hook with manual refresh. */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): { data: T | undefined; error: string | null; loading: boolean; reload: () => void } {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    setLoading(true);
    fn().then(
      (d) => {
        if (alive) {
          setData(d);
          setError(null);
          setLoading(false);
        }
      },
      (e) => {
        if (alive) {
          setError(errorMessage(e));
          setLoading(false);
        }
      },
    );
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

export function Card({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      <header className="card-head">
        <h2>{title}</h2>
        <div className="actions">{actions}</div>
      </header>
      {children}
    </section>
  );
}

export function Status({ value }: { value: string }) {
  return <span className={`badge badge-${value}`}>{value}</span>;
}

export function ErrorBox({ error }: { error: string | null }) {
  return error ? <p className="error">{error}</p> : null;
}

export function Table({ head, rows, empty = "Nothing yet." }: { head: string[]; rows: ReactNode[][]; empty?: string }) {
  if (rows.length === 0) return <p className="muted">{empty}</p>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {r.map((c, j) => (
                <td key={j}>{c}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Secret({ label, value }: { label: string; value: string }) {
  return (
    <div className="secret">
      <strong>{label}</strong> — shown once; store it securely.
      <code>{value}</code>
    </div>
  );
}
