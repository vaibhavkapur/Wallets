import { useCallback, useEffect, useState } from "react";
import type { BasketInfo, CustomerPreferences, Evaluation, Handoff, Meta, OptimizerClient, OptionResult } from "@unowallet/optimizer-sdk";
import { ApiError, formatAmount, formatDecimal } from "@unowallet/optimizer-sdk";
import { Comparison } from "./components/Comparison";
import { PreferencesForm } from "./components/PreferencesForm";

export interface AppProps {
  client: OptimizerClient;
}

const DEFAULT_CUSTOMER = "cus_demo";

function describeError(e: unknown): string {
  if (e instanceof ApiError) return `${e.code}: ${e.message}`;
  return e instanceof Error ? e.message : String(e);
}

export function App({ client }: AppProps) {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [catalog, setCatalog] = useState<BasketInfo[]>([]);
  const [customerId, setCustomerId] = useState(DEFAULT_CUSTOMER);
  const [basketKey, setBasketKey] = useState("");
  const [prefs, setPrefs] = useState<{ value: CustomerPreferences; isDefault: boolean; version: number } | null>(null);
  const [evaluation, setEvaluation] = useState<Evaluation | null>(null);
  const [handoff, setHandoff] = useState<Handoff | null>(null);
  const [approving, setApproving] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadPrefs = useCallback(
    async (id: string) => {
      const view = await client.getPreferences(id);
      setPrefs({ value: view.preferences, isDefault: view.is_default, version: view.version });
    },
    [client],
  );

  useEffect(() => {
    (async () => {
      try {
        const [m, baskets] = await Promise.all([client.meta(), client.catalog()]);
        setMeta(m);
        setCatalog(baskets);
        if (baskets[0]) setBasketKey(`${baskets[0].merchant_id}/${baskets[0].basket_ref}`);
        await loadPrefs(DEFAULT_CUSTOMER);
      } catch (e) {
        setError(describeError(e));
      }
    })();
  }, [client, loadPrefs]);

  const basket = catalog.find((b) => `${b.merchant_id}/${b.basket_ref}` === basketKey);

  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      setError(describeError(e));
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  const evaluate = () =>
    run(async () => {
      if (!basket) return;
      setHandoff(null);
      const ev = await client.evaluate({ customer_id: customerId, merchant_id: basket.merchant_id, basket_ref: basket.basket_ref });
      setEvaluation(ev);
    });

  const refresh = () =>
    run(async () => {
      if (!evaluation) return;
      setHandoff(null);
      setEvaluation(await client.refresh(evaluation.id));
    });

  const savePrefs = async (next: CustomerPreferences) => {
    await run(async () => {
      const view = await client.putPreferences(customerId, next);
      setPrefs({ value: view.preferences, isDefault: view.is_default, version: view.version });
    });
  };

  const approve = (option: OptionResult) =>
    run(async () => {
      if (!evaluation || !option.breakdown) return;
      setApproving(option.option_id);
      try {
        const h = await client.createHandoff(
          evaluation.id,
          {
            option_id: option.option_id,
            approval: {
              customer_id: evaluation.customer_id,
              approved_at: new Date().toISOString(),
              approved_amount: option.breakdown.amount_due,
              reference: `ui-${crypto.randomUUID()}`,
            },
          },
          { idempotencyKey: `ui-${crypto.randomUUID()}` },
        );
        setHandoff(h);
      } finally {
        setApproving(null);
      }
    });

  const optionIds = evaluation ? evaluation.result.options.map((o) => o.option_id) : ["card", "usdc"];

  return (
    <main>
      <h1>Card or stablecoin?</h1>
      <p className="subtle">
        Compares your eligible payment methods for the same basket by estimated net cost after fees, discounts and conservatively valued rewards.
        {meta && (
          <>
            {" "}
            Mode: <strong>{meta.mode}</strong>
            {meta.mode === "shadow" && " (recommendations are recorded; nothing is executed)"}
          </>
        )}
      </p>
      {error && (
        <div className="banner bad" role="alert">
          {error}
        </div>
      )}

      <div className="grid side">
        <div>
          <div className="panel">
            <h2>Basket</h2>
            <label htmlFor="customer">Customer id</label>
            <input
              id="customer"
              type="text"
              value={customerId}
              onChange={(e) => setCustomerId(e.target.value)}
              onBlur={() => run(() => loadPrefs(customerId))}
            />
            <label htmlFor="basket">Basket</label>
            <select id="basket" value={basketKey} onChange={(e) => setBasketKey(e.target.value)}>
              {catalog.map((b) => (
                <option key={`${b.merchant_id}/${b.basket_ref}`} value={`${b.merchant_id}/${b.basket_ref}`}>
                  {b.merchant_name}: {b.title}
                </option>
              ))}
            </select>
            {basket && (
              <table style={{ marginTop: 8 }}>
                <tbody>
                  {basket.lines.map((l) => (
                    <tr key={l.sku}>
                      <td>
                        {l.quantity} × {l.description}
                      </td>
                      <td className="amt">${formatDecimal(l.unit_price_minor, 2)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            {basket?.synthetic && <p className="subtle">Synthetic basket: illustrative data, not a merchant quote.</p>}
            <div className="row" style={{ marginTop: 12 }}>
              <button onClick={evaluate} disabled={busy || !basket}>
                Compare payment methods
              </button>
            </div>
          </div>

          <div className="panel" style={{ marginTop: 16 }}>
            {prefs ? (
              <PreferencesForm
                key={`${customerId}-${prefs.version}`}
                value={prefs.value}
                isDefault={prefs.isDefault}
                version={prefs.version}
                optionIds={optionIds}
                onSave={savePrefs}
                busy={busy}
              />
            ) : (
              <p className="subtle">Loading preferences…</p>
            )}
          </div>
        </div>

        <div>
          {evaluation ? (
            <Comparison
              evaluation={evaluation}
              onApprove={meta?.mode === "assisted" && !handoff ? approve : undefined}
              approving={approving}
              onRefresh={refresh}
              busy={busy}
            />
          ) : (
            <div className="panel">
              <p className="subtle">Choose a basket and compare. Results show immediate costs separately from estimated rewards.</p>
            </div>
          )}
          {handoff && (
            <div className={`banner ${handoff.executable ? "ok" : "warn"}`} style={{ marginTop: 12 }} data-testid="handoff">
              <strong>Approved choice handed off</strong>: {handoff.option_id} for {formatAmount(handoff.amount)} · reference <code>{handoff.id}</code> ·
              status {handoff.status} · {handoff.executable ? "executable until " + handoff.expires_at : `not executable (${handoff.not_executable_reason})`}
              <div className="subtle">The execution service revalidates this reference before charging. It is not a payment credential.</div>
            </div>
          )}
        </div>
      </div>
    </main>
  );
}
