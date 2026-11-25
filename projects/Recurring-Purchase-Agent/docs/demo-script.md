# Demo Script

[Documentation home](index.md)

Start the local stack with `make dev` and open http://127.0.0.1:5190.

## 1. Assisted Reorder Happy Path (C2)

1. In **Reorder Rules**, create the default **Morning Ethiopia Coffee** rule (12 oz, qty 1, $30.00 max delivered, $75.00 monthly cap, `C2_ASSISTED_REORDER`).
2. Click **+1 Day & Scan** in the top bar.
3. Open **Approvals & Reminders**, inspect the fresh quote ($18.50 item + $4.99 shipping + $1.53 tax = **$25.02** delivered), and click **Approve Exact Order**.
4. Open **Orders & Occurrences** to confirm `completed`, `settled`, and `confirmed`, then click **Simulate Delivered** so the next cycle is unblocked.

## 2. Payment Timeout and Recovery Without Double Charge

1. In **Operations & Sandbox Lab**, set **Payment & Fulfillment Outcome** to **Timeout (Actually Settled → Reconcile)**.
2. Click **+14 Days** to trigger the next occurrence, then approve the pending proposal.
3. Observe that the occurrence enters `needs_reconciliation` (`timeout_unknown`) and the budget reservation remains `uncertain_held`—no second charge is submitted.
4. Click **Reconcile Outcome** in **Approvals & Reminders** (or **Reconcile** in **Orders & Occurrences**). The worker queries the provider by original idempotency key and settles the existing order cleanly.

## 3. Price Hike, Pack-Size Shrink, and Substitution Guardrails

1. In **Operations & Sandbox Lab**, set **Merchant Price Multiplier** to **+45% Price Spike** and advance **+14 Days**. Confirm the proposal is marked **Policy Blocked** and cannot be approved. Decline it.
2. Reset price multiplier to **Normal (1.00×)** and set **Stock & Shrinkflation** to **Pack Size Shrunk to 10**. Advance **+14 Days**. Confirm the proposal flags **Substitution / Pack Change** and disables approval until the explicit substitution checkbox is checked.

## 4. Delayed Fulfillment Blocking Duplicate Orders

1. Set **Payment & Fulfillment Outcome** to **Delayed Fulfillment**, advance **+14 Days**, and approve the order (`Fulfillment: delayed`).
2. Advance another **+14 Days** without marking delivery. Observe in **Notification Outbox** that the duplicate reorder is blocked until the delayed order is marked delivered or canceled.

## 5. Multi-Slot Outage Coalescing

1. Mark any open order delivered and reset payment faults to **Normal Settlement**.
2. Click **Simulate 35-Day Outage** in **Operations & Sandbox Lab**.
3. Open **Approvals & Reminders**: instead of generating multiple catch-up orders, only **one** recovery proposal appears with the missed-slot count badge.

## 6. Pause vs. Cancel and Operator Emergency Pause

1. Pause a rule in **Reorder Rules**—note that future occurrences stop while any already-submitted order stays visible in **Orders & Occurrences** with its own **Cancel Order** button.
2. In **Operations & Sandbox Lab**, enter `local-operator` and click **Elevate to Operator**, then toggle **Activate Global Product Pause** to verify product-wide execution blocking.
