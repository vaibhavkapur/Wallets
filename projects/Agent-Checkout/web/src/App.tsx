import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  Clock3,
  CreditCard,
  Headphones,
  HelpCircle,
  History,
  LoaderCircle,
  LockKeyhole,
  Package,
  Plus,
  ReceiptText,
  RefreshCw,
  ShieldCheck,
  ShoppingBag,
  SlidersHorizontal,
  Sparkles,
  Truck,
  Wallet,
  X,
} from "lucide-react";
import {
  api,
  cents,
  money,
  shortID,
  type Checkout,
  type Dashboard,
  type Model,
  type Order,
} from "./api";

type Page = "checkout" | "orders" | "cards" | "activity" | "operator";
const nav = [
  { id: "checkout", label: "New checkout", icon: ShoppingBag },
  { id: "orders", label: "My orders", icon: Package },
  { id: "cards", label: "My cards", icon: CreditCard },
  { id: "activity", label: "Activity", icon: History },
] as const;
const scenarios = [
  { value: "success", label: "Successful purchase" },
  { value: "challenge", label: "Issuer authentication" },
  { value: "timeout", label: "Timeout after payment" },
  { value: "declined", label: "Payment declined" },
  { value: "merchant_failure", label: "Order fails after payment" },
  { value: "price_changed", label: "Price changes before payment" },
  { value: "out_of_stock", label: "Item goes out of stock" },
];
function Status({ value }: { value: string }) {
  const good = [
    "completed",
    "succeeded",
    "confirmed",
    "refunded",
    "enrolled",
  ].includes(value);
  const bad = ["failed", "canceled", "outcome_unknown"].includes(value);
  return (
    <span className={`status ${good ? "good" : bad ? "warn" : ""}`}>
      <span />
      {value.replaceAll("_", " ")}
    </span>
  );
}
function Modal({
  title,
  children,
  close,
}: {
  title: string;
  children: ReactNode;
  close: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement;
    const node = ref.current;
    node?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
      if (e.key === "Tab" && node) {
        const items = Array.from(
          node.querySelectorAll<HTMLElement>(
            'button:not(:disabled), input, select, [tabindex="0"]',
          ),
        );
        if (!items.length) return;
        const first = items[0],
          last = items[items.length - 1];
        if (
          e.shiftKey &&
          (document.activeElement === first || document.activeElement === node)
        ) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      previous?.focus();
    };
  }, [close]);
  return (
    <div
      className="overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close();
      }}
    >
      <div
        className="modal"
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
        tabIndex={-1}
      >
        <div className="modal-top">
          <h2 id="modal-title">{title}</h2>
          <button
            className="icon-button"
            aria-label="Close dialog"
            onClick={close}
          >
            <X size={20} />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

export default function App() {
  const [data, setData] = useState<Dashboard | null>(null),
    [page, setPage] = useState<Page>("checkout"),
    [current, setCurrent] = useState<string | null>(
      sessionStorage.getItem("uno-checkout"),
    );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [booted, setBooted] = useState(false),
    [role, setRole] = useState("customer");
  const [productID, setProductID] = useState("studio-pro"),
    [variant, setVariant] = useState("Midnight"),
    [quantity, setQuantity] = useState(1),
    [budget, setBudget] = useState("200.00"),
    [zip, setZip] = useState("94103");
  const [review, setReview] = useState(false),
    [ack, setAck] = useState(false),
    [selectedOrder, setDetail] = useState<Order | null>(null),
    [refundAmount, setRefundAmount] = useState(""),
    [refundReason, setRefundReason] = useState("Changed my mind"),
    [operatorToken, setOperatorToken] = useState(""),
    [help, setHelp] = useState(false);
  const running = useRef(false);
  const keys = useRef<Record<string, string>>({});
  const refresh = useCallback(async () => {
    const d = await api<Dashboard>("/dashboard");
    setData(d);
    return d;
  }, []);
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const session = await api<Model<"Session">>("/session", {});
        if (alive) setRole(session.role);
        const d = await api<Dashboard>("/dashboard");
        if (alive) {
          setData(d);
          setBooted(true);
        }
      } catch (e) {
        if (alive) setError((e as Error).message);
      }
    })();
    return () => {
      alive = false;
    };
  }, []);
  useEffect(() => {
    if (!booted) return;
    const timer = setInterval(() => {
      refresh().catch(() => {});
    }, 2000);
    return () => clearInterval(timer);
  }, [booted, refresh]);
  useEffect(() => {
    if (current) sessionStorage.setItem("uno-checkout", current);
    else sessionStorage.removeItem("uno-checkout");
  }, [current]);
  async function run(action: () => Promise<void>) {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      running.current = false;
      setBusy(false);
    }
  }
  const detail =
    data?.orders.find((o) => o.id === selectedOrder?.id) || selectedOrder;
  const checkout = data?.checkouts.find((c) => c.id === current),
    attempt = data?.attempts.find((a) => a.id === checkout?.attempt_id),
    order = data?.orders.find((o) => o.id === checkout?.order_id);
  const product = data?.products.find(
    (p) => p.id === (checkout?.intent.product_id || productID),
  );
  const selected = checkout?.rankings.find(
    (r) => r.card.id === checkout.card_id,
  );
  const closeReview = useCallback(() => {
      if (!running.current) setReview(false);
    }, []),
    closeDetail = useCallback(() => setDetail(null), []),
    closeHelp = useCallback(() => setHelp(false), []);
  async function create(e: FormEvent) {
    e.preventDefault();
    await run(async () => {
      const input: Model<"IntentInput"> = {
        product_id: productID,
        variant,
        quantity,
        max_total: cents(budget),
        postal_code: zip,
      };
      const intent = await api<Model<"Intent">>("/purchase-intents", input);
      const c = await api<Checkout>(
        `/purchase-intents/${intent.id}/quotes`,
        {},
      );
      setCurrent(c.id);
    });
  }
  function choose(p: Model<"Product">) {
    setProductID(p.id);
    setVariant(p.variants[0]);
    if (
      !/^\d{1,7}(\.\d{1,2})?$/.test(budget) ||
      BigInt(p.price) > BigInt(cents(budget))
    )
      setBudget((BigInt(p.price) / 100n + 40n).toString());
  }
  async function select(merchant: string, card: string, scenario: string) {
    if (!checkout) return;
    await run(async () => {
      await api(`/checkouts/${checkout.id}/selection`, {
        version: checkout.version,
        merchant_id: merchant,
        card_id: card,
        scenario,
      });
    });
  }
  async function execute() {
    if (!checkout || !ack) return;
    await run(async () => {
      const approval = await api<Model<"Consent">>(
        `/checkouts/${checkout.id}/approval`,
        { version: checkout.version },
      );
      const key = (keys.current[checkout.id] ||= crypto.randomUUID());
      await api(
        `/checkouts/${checkout.id}/execute`,
        { approval_id: approval.id },
        key,
      );
      setReview(false);
    });
  }
  function newCheckout() {
    setCurrent(null);
    setError("");
    setNotice("");
    setPage("checkout");
  }
  function openOrder(o: Order) {
    setDetail(o);
    setRefundAmount(
      ((BigInt(o.total) - BigInt(o.refunded)) / 100n).toString() +
        "." +
        ((BigInt(o.total) - BigInt(o.refunded)) % 100n)
          .toString()
          .padStart(2, "0"),
    );
    setRefundReason("Changed my mind");
  }
  async function refund(cancel = false) {
    if (!detail) return;
    await run(async () => {
      const amount = cancel ? "0" : cents(refundAmount);
      const requestKey = `refund-${detail.id}-${amount}-${refundReason}-${cancel}`;
      const key = (keys.current[requestKey] ||= crypto.randomUUID());
      await api(
        `/orders/${detail.id}/${cancel ? "cancel" : "refund-requests"}`,
        { amount, reason: refundReason },
        key,
      );
      setDetail(null);
      setNotice(
        cancel
          ? "Cancellation requested. Your refund is being processed."
          : "Refund requested. We’ll update the receipt when it is confirmed.",
      );
    });
  }
  const cardItem = (r: Model<"Ranking">, index: number) => (
    <button
      disabled={!r.eligible || busy || !!checkout?.attempt_id}
      aria-pressed={checkout?.card_id === r.card.id}
      className={`card-choice ${checkout?.card_id === r.card.id ? "selected" : ""} ${!r.eligible ? "ineligible" : ""}`}
      onClick={() =>
        checkout &&
        select(checkout.quote.merchant_id, r.card.id, checkout.scenario)
      }
      key={r.card.id}
    >
      <div className={`mini-card ${r.card.color}`}>
        <span>uno</span>
        <CreditCard size={17} />
      </div>
      <div className="card-label">
        <strong>
          {r.card.name}
          {index === 0 && r.eligible && (
            <span className="best-label">BEST VALUE</span>
          )}
        </strong>
        <span>
          {r.card.network.toUpperCase()} ···· {r.card.last4}
        </span>
        <small>{r.eligible ? r.card.protection : r.reason}</small>
      </div>
      <div className="card-return">
        <strong>{money(r.reward)}</strong>
        <span>{r.card.rate_bps / 100}% back</span>
      </div>
      <div className="radio">{checkout?.card_id === r.card.id && <span />}</div>
    </button>
  );
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a
          href="#"
          onClick={(e) => {
            e.preventDefault();
            newCheckout();
          }}
          className="brand"
        >
          <span className="brand-mark">u</span>uno
          <span className="brand-period">.</span>
        </a>
        <div className="workspace-label">YOUR WORKSPACE</div>
        <nav aria-label="Main navigation">
          {nav.map((n) => (
            <button
              key={n.id}
              aria-label={n.label}
              onClick={() => {
                if (n.id === "checkout") {
                  newCheckout();
                } else {
                  setPage(n.id);
                  setError("");
                  setNotice("");
                }
              }}
              className={page === n.id ? "active" : ""}
            >
              <n.icon size={20} />
              <span>{n.label}</span>
              {n.id === "checkout" && <Plus size={17} />}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="control-note">
            <ShieldCheck size={24} />
            <strong>You’re in control.</strong>
            <p>Every purchase waits for your approval.</p>
          </div>
          <button
            className={page === "operator" ? "side-link active" : "side-link"}
            onClick={() => setPage("operator")}
          >
            <SlidersHorizontal size={18} /> Operator console
          </button>
          <button className="side-link" onClick={() => setHelp(true)}>
            <HelpCircle size={18} /> Help & sandbox guide
          </button>
          <div className="profile">
            <div className="avatar">AM</div>
            <div>
              <strong>Alex Morgan</strong>
              <small>Personal workspace</small>
            </div>
            <ChevronDown size={15} />
          </div>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <div className="breadcrumb">
            Workspace <ChevronRight size={13} />{" "}
            <strong>
              {page === "checkout"
                ? "Agent checkout"
                : nav.find((n) => n.id === page)?.label || "Operator console"}
            </strong>
          </div>
          <div className="top-actions">
            <span className="sandbox-tag">
              <span /> Sandbox mode
            </span>
            <button
              className="icon-button"
              aria-label="Open help"
              onClick={() => setHelp(true)}
            >
              <HelpCircle size={19} />
            </button>
          </div>
        </header>
        <main>
          <div className="page-heading">
            <div>
              <span className="eyebrow">
                {page === "checkout"
                  ? "SHOP WITH A LITTLE EXTRA CONFIDENCE"
                  : "YOUR UNO WORKSPACE"}
              </span>
              <h1>
                {page === "checkout"
                  ? checkout
                    ? "All the details. One good decision."
                    : "Your next purchase, sorted."
                  : page === "orders"
                    ? "Good things, on their way."
                    : page === "cards"
                      ? "The right card for every purchase."
                      : page === "activity"
                        ? "Every step, accounted for."
                        : "Keep every purchase on track."}
              </h1>
              <p>
                {page === "checkout"
                  ? "Find your item, compare your cards, and approve exactly what you want."
                  : page === "orders"
                    ? "Receipts, delivery updates, and returns, all in one place."
                    : page === "cards"
                      ? "Compare eligible rewards and protections before you pay."
                      : page === "activity"
                        ? "A clear record of approvals, payments, and refunds."
                        : "Review payment outcomes, reservations, and recovery jobs."}
              </p>
            </div>
            {page === "orders" && (
              <button className="primary small" onClick={newCheckout}>
                <Plus size={17} /> New checkout
              </button>
            )}
          </div>
          {error && (
            <div className="alert error" role="alert">
              <HelpCircle size={19} />
              <span>{error}</span>
              <button aria-label="Dismiss error" onClick={() => setError("")}>
                <X size={16} />
              </button>
            </div>
          )}
          {notice && (
            <div className="alert success" role="status">
              <CircleCheck size={19} />
              {notice}
            </div>
          )}
          {!data ? (
            <div className="loading">
              <LoaderCircle className="spin" />
              <h2>Getting your workspace ready</h2>
              {error && (
                <button
                  className="secondary"
                  onClick={() => window.location.reload()}
                >
                  Try again
                </button>
              )}
            </div>
          ) : (
            <>
              {data.paused && (
                <div className="alert error">
                  <LockKeyhole size={18} /> New purchases are paused. Existing
                  payments and refunds will continue to reconcile.
                </div>
              )}
              {page === "checkout" && (
                <>
                  <div className="stepper">
                    <div className="done">
                      <span>{checkout ? <Check size={14} /> : 1}</span>Choose
                      your item
                    </div>
                    <i />
                    <div className={checkout ? "done" : ""}>
                      <span>{order ? <Check size={14} /> : 2}</span>Review &
                      approve
                    </div>
                    <i />
                    <div className={order ? "done" : ""}>
                      <span>3</span>Track your order
                    </div>
                    <span className="stepper-end">
                      <LockKeyhole size={13} /> Always with your approval
                    </span>
                  </div>
                  {!checkout ? (
                    <div className="checkout-grid">
                      <section className="panel create-panel">
                        <div className="section-head">
                          <div>
                            <span className="eyebrow">01 / THE FIND</span>
                            <h2>What’s on your list?</h2>
                          </div>
                          <span className="subtle-label">
                            <Headphones size={15} /> Audio essentials
                          </span>
                        </div>
                        <p className="muted intro">
                          Start with an exact model. We’ll take care of the
                          comparison.
                        </p>
                        <form onSubmit={create}>
                          <div className="product-options">
                            {data.products.map((p) => (
                              <button
                                type="button"
                                key={p.id}
                                aria-pressed={productID === p.id}
                                className={`product-option ${productID === p.id ? "selected" : ""}`}
                                onClick={() => choose(p)}
                              >
                                <span className="product-art">
                                  <Headphones
                                    size={p.id === "air-mini" ? 38 : 52}
                                    strokeWidth={1.4}
                                  />
                                  {productID === p.id && (
                                    <span className="product-check">
                                      <Check size={12} />
                                    </span>
                                  )}
                                </span>
                                <small>{p.brand}</small>
                                <strong>{p.name}</strong>
                                <span>From {money(p.price)}</span>
                              </button>
                            ))}
                          </div>
                          <div className="field-grid">
                            <label>
                              Color
                              <select
                                value={variant}
                                onChange={(e) => setVariant(e.target.value)}
                              >
                                {data.products
                                  .find((p) => p.id === productID)
                                  ?.variants.map((v) => (
                                    <option key={v}>{v}</option>
                                  ))}
                              </select>
                            </label>
                            <label>
                              Quantity
                              <select
                                value={quantity}
                                onChange={(e) =>
                                  setQuantity(Number(e.target.value))
                                }
                              >
                                {[1, 2, 3, 4, 5].map((v) => (
                                  <option key={v}>{v}</option>
                                ))}
                              </select>
                            </label>
                          </div>
                          <div className="field-grid">
                            <label>
                              Maximum delivered price
                              <div className="input-prefix">
                                <span>$</span>
                                <input
                                  aria-label="Maximum delivered price"
                                  inputMode="decimal"
                                  value={budget}
                                  onChange={(e) => setBudget(e.target.value)}
                                  required
                                />
                              </div>
                              <small>
                                Including shipping and estimated tax
                              </small>
                            </label>
                            <label>
                              Delivery ZIP code
                              <input
                                value={zip}
                                onChange={(e) => setZip(e.target.value)}
                                pattern="[0-9]{5}"
                                maxLength={5}
                                inputMode="numeric"
                                required
                              />
                              <small>
                                Sandbox delivery in the United States
                              </small>
                            </label>
                          </div>
                          <div className="form-footer">
                            <span>
                              <ShieldCheck size={16} /> Nothing is purchased
                              yet.
                            </span>
                            <button
                              className="primary"
                              disabled={busy || data.paused}
                            >
                              {busy ? (
                                <LoaderCircle className="spin" size={17} />
                              ) : (
                                <Sparkles size={17} />
                              )}{" "}
                              Find my checkout
                            </button>
                          </div>
                        </form>
                      </section>
                      <aside className="checkout-aside">
                        <div className="promise-panel">
                          <span className="promise-icon">
                            <Sparkles size={23} />
                          </span>
                          <span className="eyebrow">
                            A SMARTER WAY TO CHECK OUT
                          </span>
                          <h2>
                            A good find.
                            <br />
                            An even better way
                            <br />
                            to pay.
                          </h2>
                          <p>
                            We compare the delivered price and your eligible
                            card rewards. You make the final call.
                          </p>
                          <div className="promise-facts">
                            <span>
                              <Check size={16} /> Three approved sandbox
                              retailers
                            </span>
                            <span>
                              <Check size={16} /> Rewards with clear assumptions
                            </span>
                            <span>
                              <Check size={16} /> No purchase without your
                              approval
                            </span>
                          </div>
                        </div>
                        <BudgetCard data={data} />
                        <div className="sandbox-note">
                          <span className="tiny-label">
                            MADE FOR A SAFE TEST DRIVE
                          </span>
                          <p>
                            Sample products, merchants, and cards. No real money
                            moves in this workspace.
                          </p>
                        </div>
                      </aside>
                    </div>
                  ) : (
                    <div className="checkout-grid">
                      <div className="checkout-main">
                        <section className="panel product-summary">
                          <div className="summary-art">
                            <Headphones size={58} strokeWidth={1.4} />
                          </div>
                          <div>
                            <span className="eyebrow">{product?.brand}</span>
                            <h2>{product?.name}</h2>
                            <p>
                              {checkout.intent.variant} <span>·</span> Qty{" "}
                              {checkout.intent.quantity} <span>·</span>{" "}
                              {product?.description.split(" · ")[0]}
                            </p>
                            <small>
                              Deliver to ZIP {checkout.intent.postal_code} ·
                              Limit {money(checkout.intent.max_total)}
                            </small>
                          </div>
                          <button className="text-button" onClick={newCheckout}>
                            New search
                          </button>
                        </section>
                        {checkout.attempt_id ? (
                          <section className="panel payment-progress">
                            <div
                              className={`progress-icon ${order ? "complete" : ""}`}
                            >
                              {order ? (
                                <Check size={28} />
                              ) : attempt?.status === "failed" ? (
                                <X size={28} />
                              ) : (
                                <Clock3 size={28} />
                              )}
                            </div>
                            <span className="eyebrow">
                              {order ? "PURCHASE RECEIPT" : "PAYMENT STATUS"}
                            </span>
                            <h2>
                              {order
                                ? order.fulfillment_status === "failed"
                                  ? "Payment received. Order needs attention."
                                  : "A good purchase, approved by you."
                                : attempt?.status === "requires_customer_action"
                                  ? "One quick check with your issuer."
                                  : attempt?.status === "outcome_unknown"
                                    ? "Confirming your payment."
                                    : attempt?.status === "failed"
                                      ? "Your payment was declined."
                                      : "Your checkout is in progress."}
                            </h2>
                            <p>
                              {order
                                ? order.fulfillment_status === "failed"
                                  ? "The merchant could not confirm the order. Request a full refund below."
                                  : `${order.merchant} has confirmed your order. ${order.delivery}.`
                                : attempt?.status === "requires_customer_action"
                                  ? "Use the sandbox issuer controls to complete or decline the authentication challenge."
                                  : attempt?.status === "outcome_unknown"
                                    ? "The provider timed out. Your budget is reserved while we check the result. You will not be charged again."
                                    : attempt?.status === "failed"
                                      ? "The provider confirmed the payment failed. Your reserved budget has been released."
                                      : "We’re securely coordinating your payment and merchant order."}
                            </p>
                            {attempt && <Status value={attempt.status} />}
                            <div className="progress-actions">
                              {attempt?.status ===
                                "requires_customer_action" && (
                                <>
                                  <button
                                    className="primary"
                                    disabled={busy}
                                    onClick={() =>
                                      run(async () => {
                                        await api(
                                          `/checkouts/${checkout.id}/challenge`,
                                          { success: true },
                                        );
                                      })
                                    }
                                  >
                                    <ShieldCheck size={17} /> Complete sandbox
                                    verification
                                  </button>
                                  <button
                                    className="secondary"
                                    disabled={busy}
                                    onClick={() =>
                                      run(async () => {
                                        await api(
                                          `/checkouts/${checkout.id}/challenge`,
                                          { success: false },
                                        );
                                      })
                                    }
                                  >
                                    Decline verification
                                  </button>
                                </>
                              )}
                              {attempt?.status === "outcome_unknown" && (
                                <button
                                  className="secondary"
                                  disabled={busy}
                                  onClick={() =>
                                    run(async () => {
                                      await api(
                                        `/checkouts/${checkout.id}/reconcile`,
                                        {},
                                      );
                                      setNotice(
                                        "Reconciliation queued. We’ll check the existing payment.",
                                      );
                                    })
                                  }
                                >
                                  <RefreshCw size={16} /> Check payment status
                                </button>
                              )}
                              {order && (
                                <button
                                  className="primary"
                                  onClick={() => openOrder(order)}
                                >
                                  <ReceiptText size={17} /> View receipt
                                </button>
                              )}
                              {attempt?.status === "failed" && (
                                <button
                                  className="primary"
                                  onClick={newCheckout}
                                >
                                  Start a new checkout
                                </button>
                              )}
                            </div>
                          </section>
                        ) : (
                          <>
                            <section className="panel offers-panel">
                              <div className="section-head">
                                <div>
                                  <span className="eyebrow">
                                    02 / THE RETAILER
                                  </span>
                                  <h2>Your item. Three ways to get it.</h2>
                                </div>
                                <span className="subtle-label">
                                  <RefreshCw size={13} /> 15-minute quote
                                </span>
                              </div>
                              <div className="offers">
                                {checkout.quotes.map((q, i) => (
                                  <button
                                    key={q.id}
                                    disabled={
                                      busy ||
                                      BigInt(q.total) >
                                        BigInt(checkout.intent.max_total)
                                    }
                                    aria-pressed={
                                      q.merchant_id ===
                                      checkout.quote.merchant_id
                                    }
                                    className={`offer ${q.merchant_id === checkout.quote.merchant_id ? "selected" : ""}`}
                                    onClick={() =>
                                      select(
                                        q.merchant_id,
                                        checkout.card_id,
                                        checkout.scenario,
                                      )
                                    }
                                  >
                                    <span className="merchant-icon">
                                      {q.merchant[0]}
                                    </span>
                                    <span className="merchant-name">
                                      <strong>
                                        {q.merchant}
                                        {i === 0 && (
                                          <span className="best-label">
                                            LOWEST TOTAL
                                          </span>
                                        )}
                                      </strong>
                                      <small>
                                        <Truck size={13} />
                                        {q.delivery.replace("Arrives in ", "")}
                                      </small>
                                    </span>
                                    <span className="offer-price">
                                      <strong>{money(q.total)}</strong>
                                      <small>
                                        {q.shipping === "0"
                                          ? "Free delivery"
                                          : `${money(q.shipping)} delivery`}
                                      </small>
                                    </span>
                                    <div className="radio">
                                      {q.merchant_id ===
                                        checkout.quote.merchant_id && <span />}
                                    </div>
                                  </button>
                                ))}
                              </div>
                              <div className="panel-footnote">
                                <ShieldCheck size={14} /> 30-day returns · Price
                                and stock checked again before payment
                              </div>
                            </section>
                            <section className="panel cards-panel">
                              <div className="section-head">
                                <div>
                                  <span className="eyebrow">
                                    03 / THE RIGHT CARD
                                  </span>
                                  <h2>Make the most of this purchase.</h2>
                                </div>
                                <CreditCard size={23} className="muted" />
                              </div>
                              <div className="card-list">
                                {checkout.rankings.map(cardItem)}
                              </div>
                              <div className="reward-disclosure">
                                <HelpCircle size={15} />
                                <p>
                                  Estimated rewards use illustrative rules,
                                  version <strong>sandbox-2026-10-v1</strong>,
                                  at 1¢ per reward cent. Rewards include tax and
                                  shipping and round down to whole cents. Actual
                                  eligibility and rewards are unverified.
                                </p>
                              </div>
                            </section>
                          </>
                        )}
                        <section className="trust-strip">
                          <div>
                            <ShieldCheck size={22} />
                            <span>
                              <strong>Scoped to this purchase</strong>
                              <small>Only this merchant, card, and total</small>
                            </span>
                          </div>
                          <div>
                            <LockKeyhole size={21} />
                            <span>
                              <strong>Your card stays private</strong>
                              <small>
                                No card numbers collected in this demo
                              </small>
                            </span>
                          </div>
                        </section>
                      </div>
                      <aside className="checkout-aside">
                        <section className="panel order-summary">
                          <span className="eyebrow">YOUR CHECKOUT</span>
                          <div className="summary-title">
                            <h2>A clear total.</h2>
                            <ShoppingBag size={22} />
                          </div>
                          <div className="summary-lines">
                            <span>
                              Items ({checkout.intent.quantity})
                              <strong>{money(checkout.quote.subtotal)}</strong>
                            </span>
                            <span>
                              Delivery
                              <strong
                                className={
                                  checkout.quote.shipping === "0"
                                    ? "green-text"
                                    : ""
                                }
                              >
                                {checkout.quote.shipping === "0"
                                  ? "Free"
                                  : money(checkout.quote.shipping)}
                              </strong>
                            </span>
                            <span>
                              Estimated tax
                              <strong>{money(checkout.quote.tax)}</strong>
                            </span>
                          </div>
                          <div className="total-line">
                            <span>Total to pay</span>
                            <strong>{money(checkout.quote.total)}</strong>
                          </div>
                          <div className="reward-box">
                            <Sparkles size={18} />
                            <div>
                              <strong>
                                {money(selected?.reward || "0")} in estimated
                                rewards
                              </strong>
                              <span>with {selected?.card.name}</span>
                            </div>
                          </div>
                          <div className="effective-line">
                            After estimated rewards
                            <strong>
                              {money(
                                selected?.effective_total ||
                                  checkout.quote.total,
                              )}
                            </strong>
                          </div>
                          <div className="summary-divider" />
                          <div className="delivery-info">
                            <Truck size={18} />
                            <div>
                              <strong>{checkout.quote.delivery}</strong>
                              <span>
                                Standard delivery · ZIP{" "}
                                {checkout.intent.postal_code}
                              </span>
                            </div>
                          </div>
                          <div className="delivery-info">
                            <Package size={18} />
                            <div>
                              <strong>Room to change your mind</strong>
                              <span>{checkout.quote.return_terms}</span>
                            </div>
                          </div>
                          {!checkout.attempt_id &&
                          checkout.state !== "canceled" ? (
                            <>
                              <button
                                className="primary full"
                                disabled={
                                  busy ||
                                  data.paused ||
                                  Date.parse(checkout.quote.expires_at) <=
                                    Date.now()
                                }
                                onClick={() => {
                                  setAck(false);
                                  setReview(true);
                                }}
                              >
                                <LockKeyhole size={16} /> Review & approve
                              </button>
                              <p className="approval-note">
                                {Date.parse(checkout.quote.expires_at) <=
                                Date.now()
                                  ? "This quote expired. Start a new checkout for a fresh price."
                                  : "You’ll approve the exact amount before payment."}
                              </p>
                              <button
                                className="text-button cancel-checkout"
                                disabled={busy}
                                onClick={() =>
                                  run(async () => {
                                    await api(
                                      `/checkouts/${checkout.id}/cancel`,
                                      {},
                                    );
                                    setNotice(
                                      "Checkout canceled. Your approval has been revoked.",
                                    );
                                  })
                                }
                              >
                                Cancel this checkout
                              </button>
                            </>
                          ) : (
                            <div className="summary-state">
                              <Status value={checkout.state} />
                              <small>Checkout {shortID(checkout.id)}</small>
                            </div>
                          )}
                        </section>
                        {!checkout.attempt_id && (
                          <details className="scenario-control">
                            <summary>
                              <SlidersHorizontal size={15} /> Sandbox scenario
                            </summary>
                            <label>
                              Next payment behavior
                              <select
                                value={checkout.scenario}
                                disabled={busy}
                                onChange={(e) =>
                                  select(
                                    checkout.quote.merchant_id,
                                    checkout.card_id,
                                    e.target.value,
                                  )
                                }
                              >
                                {scenarios.map((s) => (
                                  <option value={s.value} key={s.value}>
                                    {s.label}
                                  </option>
                                ))}
                              </select>
                            </label>
                            <p>
                              Changing this setting creates a new checkout
                              version.
                            </p>
                          </details>
                        )}
                        <BudgetCard data={data} />
                      </aside>
                    </div>
                  )}
                </>
              )}
              {page === "orders" && (
                <section className="panel orders-panel">
                  <div className="section-head">
                    <h2>
                      All orders{" "}
                      <span className="count">{data.orders.length}</span>
                    </h2>
                    <span className="muted">
                      Payment and delivery tracked separately
                    </span>
                  </div>
                  {data.orders.length === 0 ? (
                    <Empty
                      icon={<Package size={34} />}
                      title="Your first good find belongs here."
                      text="After you approve a purchase, we’ll keep the receipt and delivery status right here."
                      action={
                        <button className="primary" onClick={newCheckout}>
                          <Plus size={17} /> Start a checkout
                        </button>
                      }
                    />
                  ) : (
                    <div className="order-list">
                      {data.orders.map((o) => (
                        <button
                          key={o.id}
                          className="order-row"
                          onClick={() => openOrder(o)}
                        >
                          <div className="order-product-icon">
                            <Headphones size={28} />
                          </div>
                          <div className="order-description">
                            <strong>{o.product}</strong>
                            <span>
                              {o.merchant} · {o.variant} · Qty {o.quantity}
                            </span>
                            <small>
                              {new Date(o.created_at).toLocaleDateString(
                                undefined,
                                {
                                  day: "numeric",
                                  month: "short",
                                  year: "numeric",
                                },
                              )}{" "}
                              · {shortID(o.id)}
                            </small>
                          </div>
                          <div>
                            <small>Payment</small>
                            <Status value={o.payment_status} />
                          </div>
                          <div>
                            <small>Delivery</small>
                            <Status value={o.fulfillment_status} />
                          </div>
                          <strong>{money(o.total)}</strong>
                          <ChevronRight size={17} />
                        </button>
                      ))}
                    </div>
                  )}
                </section>
              )}
              {page === "cards" && (
                <>
                  <div className="wallet-grid">
                    {data.cards.map((c) => (
                      <section className="panel wallet-card" key={c.id}>
                        <div className={`physical-card ${c.color}`}>
                          <div>
                            <strong>uno.</strong>
                            <span>{c.name}</span>
                          </div>
                          <CreditCard size={27} strokeWidth={1} />
                          <div>
                            <span>
                              •••• &nbsp; •••• &nbsp; •••• &nbsp; {c.last4}
                            </span>
                            <strong>{c.network.toUpperCase()}</strong>
                          </div>
                        </div>
                        <div className="wallet-card-meta">
                          <h2>{c.name}</h2>
                          <span>{c.rate_bps / 100}% estimated cashback</span>
                          <p>
                            <ShieldCheck size={16} />
                            {c.protection}
                          </p>
                          {c.enrolled ? (
                            <Status value="enrolled" />
                          ) : (
                            <button
                              disabled={busy}
                              className="secondary full"
                              onClick={() =>
                                run(async () => {
                                  await api(`/cards/${c.id}/enroll`, {});
                                  setNotice(
                                    "Sandbox card enrolled. Start a new checkout to compare it.",
                                  );
                                })
                              }
                            >
                              <Plus size={16} /> Enroll sandbox card
                            </button>
                          )}
                        </div>
                      </section>
                    ))}
                  </div>
                  <div className="info-panel">
                    <LockKeyhole size={24} />
                    <div>
                      <h3>Card details belong with your payment provider.</h3>
                      <p>
                        These are sample card references. Sandbox enrollment
                        does not ask for a card number. A live launch requires a
                        provider-hosted enrollment flow and verified access to
                        scoped payment credentials.
                      </p>
                    </div>
                  </div>
                </>
              )}
              {page === "activity" && (
                <section className="panel activity-panel">
                  <div className="section-head">
                    <h2>Your purchase timeline</h2>
                    <span className="subtle-label">
                      {data.audit.length} events
                    </span>
                  </div>
                  {data.audit.length === 0 ? (
                    <Empty
                      icon={<History size={34} />}
                      title="A fresh start."
                      text="Create a checkout to see every approval, payment, and refund recorded here."
                    />
                  ) : (
                    <div className="timeline">
                      {data.audit.map((a) => (
                        <div className="timeline-event" key={a.id}>
                          <div className="timeline-icon">
                            {a.kind.includes("approval") ? (
                              <ShieldCheck size={17} />
                            ) : a.kind.includes("payment") ? (
                              <CreditCard size={17} />
                            ) : (
                              <ReceiptText size={17} />
                            )}
                          </div>
                          <div>
                            <strong>
                              {a.kind
                                .replaceAll(".", " · ")
                                .replaceAll("_", " ")}
                            </strong>
                            <p>{a.detail}</p>
                            <small>{shortID(a.entity_id)}</small>
                          </div>
                          <time>
                            {new Date(a.at).toLocaleTimeString(undefined, {
                              hour: "2-digit",
                              minute: "2-digit",
                            })}
                          </time>
                        </div>
                      ))}
                    </div>
                  )}
                </section>
              )}
              {page === "operator" &&
                (role !== "operator" ? (
                  <section className="panel operator-login">
                    <div className="progress-icon">
                      <LockKeyhole size={28} />
                    </div>
                    <h2>Operator access</h2>
                    <p>
                      Enter the operator token configured for this sandbox
                      server to manage the pause control and inspect the ledger.
                    </p>
                    <form
                      onSubmit={(e) => {
                        e.preventDefault();
                        run(async () => {
                          const session = await api<Model<"Session">>(
                            "/session",
                            { role: "operator", token: operatorToken },
                          );
                          setRole(session.role);
                          setOperatorToken("");
                        });
                      }}
                    >
                      <label>
                        Operator token
                        <input
                          type="password"
                          autoComplete="off"
                          value={operatorToken}
                          onChange={(e) => setOperatorToken(e.target.value)}
                          required
                        />
                      </label>
                      <button className="primary" disabled={busy}>
                        Open console
                      </button>
                    </form>
                  </section>
                ) : (
                  <>
                    <div className="operator-stats">
                      <Metric
                        label="Available budget"
                        value={money(data.budget.available)}
                      />
                      <Metric
                        label="Reserved"
                        value={money(data.budget.reserved)}
                      />
                      <Metric
                        label="Net captured"
                        value={money(data.budget.spent)}
                      />
                      <Metric
                        label="Jobs pending"
                        value={String(
                          data.jobs.filter((j) => j.status !== "done").length,
                        )}
                      />
                    </div>
                    <section className="panel operator-controls">
                      <div>
                        <h2>Purchase controls</h2>
                        <p>
                          {data.paused
                            ? "New approvals and payments are paused."
                            : "The sandbox is accepting customer-approved purchases."}
                        </p>
                      </div>
                      <button
                        className={data.paused ? "primary" : "secondary"}
                        disabled={busy}
                        onClick={() =>
                          run(async () => {
                            await api("/operator/pause", {
                              paused: !data.paused,
                            });
                          })
                        }
                      >
                        {data.paused ? "Resume purchases" : "Pause purchases"}
                      </button>
                    </section>
                    <section className="panel">
                      <div className="section-head">
                        <h2>Payment reconciliation</h2>
                        <span className="muted">
                          Authoritative provider lookup
                        </span>
                      </div>
                      {data.attempts.length === 0 ? (
                        <Empty
                          icon={<RefreshCw size={26} />}
                          title="No payments to reconcile"
                          text="Payment attempts will appear after a customer approves a checkout."
                        />
                      ) : (
                        data.attempts.map((a) => (
                          <div className="ops-row" key={a.id}>
                            <div>
                              <strong>{shortID(a.id)}</strong>
                              <small>{shortID(a.checkout_id)}</small>
                            </div>
                            <Status value={a.status} />
                            <strong>{money(a.amount)}</strong>
                            <button
                              className="secondary small"
                              disabled={
                                busy ||
                                ["succeeded", "failed"].includes(a.status)
                              }
                              onClick={() =>
                                run(async () => {
                                  await api(
                                    `/checkouts/${a.checkout_id}/reconcile`,
                                    {},
                                  );
                                  setNotice("Provider lookup queued.");
                                })
                              }
                            >
                              Reconcile
                            </button>
                          </div>
                        ))
                      )}
                    </section>
                    <section className="panel ledger-panel">
                      <div className="section-head">
                        <h2>Budget ledger</h2>
                        <span className="muted">
                          {data.ledger.length} entries
                        </span>
                      </div>
                      {data.ledger.length === 0 ? (
                        <p className="muted pad">
                          No reservations or captures yet.
                        </p>
                      ) : (
                        data.ledger
                          .slice()
                          .reverse()
                          .map((l) => (
                            <div className="ops-row" key={l.id}>
                              <span className="ledger-kind">{l.kind}</span>
                              <span className="muted">
                                {shortID(l.entity_id)}
                              </span>
                              <strong>{money(l.amount)}</strong>
                              <time>{new Date(l.at).toLocaleTimeString()}</time>
                            </div>
                          ))
                      )}
                    </section>
                  </>
                ))}
            </>
          )}
          <footer>
            <span>
              <ShieldCheck size={14} /> Built around your approval.
            </span>
            <span>
              Uno Agent Checkout <span className="footer-dot">·</span> Sandbox
              MVP
            </span>
          </footer>
        </main>
      </div>
      {review && checkout && (
        <Modal title="One last look. Your call." close={closeReview}>
          <div className="modal-product">
            <div className="order-product-icon">
              <Headphones size={30} />
            </div>
            <div>
              <strong>{product?.name}</strong>
              <span>
                {checkout.intent.variant} · Qty {checkout.intent.quantity} ·{" "}
                {checkout.quote.merchant}
              </span>
            </div>
          </div>
          <div className="approval-details">
            <span>
              Delivered total<strong>{money(checkout.quote.total)} USD</strong>
            </span>
            <span>
              Pay with
              <strong>
                {selected?.card.name} ···· {selected?.card.last4}
              </strong>
            </span>
            <span>
              Estimated rewards<strong>{money(selected?.reward || "0")}</strong>
            </span>
            <span>
              Delivery<strong>{checkout.quote.delivery}</strong>
            </span>
            <span>
              Approval bound to
              <strong>Checkout version {checkout.version}</strong>
            </span>
          </div>
          <p className="modal-terms">{checkout.quote.return_terms}</p>
          <label className="consent-checkbox">
            <input
              type="checkbox"
              checked={ack}
              onChange={(e) => setAck(e.target.checked)}
            />
            <span>
              I approve this one-time sandbox purchase from{" "}
              {checkout.quote.merchant} for{" "}
              <strong>{money(checkout.quote.total)}</strong> using the selected
              card.
            </span>
          </label>
          {error && (
            <div className="alert error" role="alert">
              {error}
            </div>
          )}
          <button
            className="primary full"
            disabled={!ack || busy}
            onClick={execute}
          >
            {busy ? (
              <LoaderCircle className="spin" size={18} />
            ) : (
              <LockKeyhole size={17} />
            )}{" "}
            Approve & pay {money(checkout.quote.total)}
          </button>
          <p className="approval-note">
            No real charge. This approval expires in 5 minutes.
          </p>
        </Modal>
      )}
      {detail && (
        <Modal title="Your purchase receipt" close={closeDetail}>
          <div className="receipt-header">
            <CircleCheck size={30} />
            <strong>{detail.product}</strong>
            <span>
              {detail.merchant} · Order {shortID(detail.id)}
            </span>
          </div>
          <div className="approval-details">
            <span>
              Paid<strong>{money(detail.total)}</strong>
            </span>
            <span>
              Card
              <strong>
                {detail.card_name} ···· {detail.card_last4}
              </strong>
            </span>
            <span>
              Payment
              <Status
                value={
                  data?.orders.find((o) => o.id === detail.id)
                    ?.payment_status || detail.payment_status
                }
              />
            </span>
            <span>
              Fulfillment
              <Status
                value={
                  data?.orders.find((o) => o.id === detail.id)
                    ?.fulfillment_status || detail.fulfillment_status
                }
              />
            </span>
            <span>
              Refunded
              <strong>
                {money(
                  data?.orders.find((o) => o.id === detail.id)?.refunded ||
                    detail.refunded,
                )}
              </strong>
            </span>
          </div>
          {data?.refunds
            .filter((r) => r.order_id === detail.id)
            .map((r) => (
              <div className="refund-line" key={r.id}>
                <span>Refund · {money(r.amount)}</span>
                <Status value={r.status} />
              </div>
            ))}
          {role === "operator" &&
            ["confirmed", "shipped"].includes(detail.fulfillment_status) &&
            detail.payment_status !== "refunded" && (
              <button
                className="secondary full"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await api(`/operator/orders/${detail.id}/advance`, {});
                  })
                }
              >
                Sandbox:{" "}
                {detail.fulfillment_status === "confirmed"
                  ? "mark shipped"
                  : "mark delivered"}
              </button>
            )}
          {BigInt(detail.refunded) < BigInt(detail.total) && (
            <div className="refund-form">
              <h3>Need to return or cancel?</h3>
              <p className="muted">
                Sandbox refunds return to the original card. Full and partial
                refunds are supported within 30 days.
              </p>
              <div className="field-grid">
                <label>
                  Refund amount ($)
                  <input
                    aria-label="Refund amount"
                    inputMode="decimal"
                    value={refundAmount}
                    onChange={(e) => setRefundAmount(e.target.value)}
                  />
                </label>
                <label>
                  Reason
                  <select
                    value={refundReason}
                    onChange={(e) => setRefundReason(e.target.value)}
                  >
                    <option>Changed my mind</option>
                    <option>Wrong item</option>
                    <option>Item damaged</option>
                    <option>Merchant order failed</option>
                  </select>
                </label>
              </div>
              {error && (
                <div className="alert error" role="alert">
                  {error}
                </div>
              )}
              <div className="refund-actions">
                <button
                  className="primary"
                  disabled={busy}
                  onClick={() => refund()}
                >
                  Request refund
                </button>
                {["confirmed", "failed"].includes(
                  detail.fulfillment_status,
                ) && (
                  <button
                    className="secondary"
                    disabled={busy}
                    onClick={() => refund(true)}
                  >
                    Cancel & refund remaining
                  </button>
                )}
              </div>
            </div>
          )}
        </Modal>
      )}
      {help && (
        <Modal title="A safe place to try checkout." close={closeHelp}>
          <div className="help-copy">
            <p>
              This is a working sandbox of Agent Checkout. Products, retailers,
              card profiles, delivery estimates, tax, and reward rules are
              illustrative.
            </p>
            <h3>Try the full flow</h3>
            <ol>
              <li>Choose a model, color, delivery ZIP, and spending limit.</li>
              <li>Compare the delivered offers and eligible cards.</li>
              <li>Review the exact details and approve the purchase.</li>
              <li>
                Find the receipt in My orders. Try cancellation or a partial
                refund.
              </li>
            </ol>
            <h3>Explore the unexpected</h3>
            <p>
              Open “Sandbox scenario” on a quoted checkout to try an issuer
              challenge, a timeout, a decline, a changed quote, or a merchant
              order failure.
            </p>
            <p>
              A timeout keeps the budget reserved until a provider lookup
              confirms the result. It never creates a second charge.
            </p>
            <div className="info-panel compact">
              <ShieldCheck size={22} />
              <p>
                No live payment credentials are collected. This sandbox does not
                establish real merchant or payment-provider access.
              </p>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
function BudgetCard({ data }: { data: Dashboard }) {
  return (
    <section className="budget-card">
      <div>
        <span className="budget-icon">
          <Wallet size={18} />
        </span>
        <strong>Your sandbox budget</strong>
      </div>
      <div className="budget-value">
        {money(data.budget.available)}
        <span>available of {money(data.budget.limit)}</span>
      </div>
      <div className="budget-bar">
        <span
          style={{
            width: `${Math.min(100, Number(((BigInt(data.budget.spent) + BigInt(data.budget.reserved)) * 100n) / BigInt(data.budget.limit)))}%`,
          }}
        />
      </div>
      <small>
        {BigInt(data.budget.reserved) > 0
          ? `${money(data.budget.reserved)} reserved for pending payments`
          : "A spending limit that stays in your hands."}
      </small>
    </section>
  );
}
function Empty({
  icon,
  title,
  text,
  action,
}: {
  icon: ReactNode;
  title: string;
  text: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <h2>{title}</h2>
      <p>{text}</p>
      {action}
    </div>
  );
}
function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="panel metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
