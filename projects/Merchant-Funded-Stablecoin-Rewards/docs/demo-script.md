# Demo Script

[Documentation home](index.md)

Start the local stack with `make dev` and open [http://127.0.0.1:5189](http://127.0.0.1:5189). All scenarios below run against deterministic sandbox fixtures.

## 1. Happy-Path Claim & Double-Entry Audit

1. In **Customer Rewards** (`cust_alex`), confirm `ORD-9001` (`$10.00`) is **eligible** and `ORD-9002` (`$15.00`) is **pending**.
2. Click **Get Payout Quote**. Verify exact USD (`$10.00`), gross USDC (`10.000000 USDC`), fee (`-0.250000 USDC`), net payout (`9.750000 USDC`), locked beneficiary (`ben_cust_alex_v1`), and disclosure.
3. Approve terms and click **Authorize Payout**. Confirm `ORD-9001` transitions to `paid` and the payout attempt shows `succeeded` with 12 confirmations.
4. Switch to **Operator Queue** and inspect **Double-Entry Ledger Trial Balance** (`USD/fiat` and `USDC/base` both balanced) and the **Recent Immutable Journal Entries** log.

## 2. Partial Return Before Payout & Separate Fulfillment

1. Switch to **Merchant Campaigns**.
2. Under **Simulate Signed Merchant Event**, send `order.paid` for `Roast & Co. Coffee` (`cmp_roast_spring`), `Order ID: ORD-9100`, `Customer ID: cust_alex`, `Subtotal: 200.00`, with **immediately fulfilled UNCHECKED**. A `$10.00` pending reward (`5%`) is reserved.
3. Send `order.refunded` for `ORD-9100` with `Refund Amount: 50.00` (25% return). In **Customer Rewards**, confirm `ORD-9100` shows `Original: $10.00`, `Reversed: $2.50`, `Net: $7.50`, and `$2.50` plus pro-rata fee/reserve returned to campaign available budget.
4. In **Operator Queue**, click **Advance Clock +15d**. Because `ORD-9100` is still unfulfilled, it remains **pending** (payment settlement does not imply fulfillment).
5. Back in **Merchant Campaigns**, send `order.fulfilled` for `ORD-9100`, then click **Advance Clock +15d** in **Operator Queue**. `ORD-9100` now becomes **eligible**.

## 3. Timeout Recovery (`outcome_unknown`) Without Double-Pay

1. In **Customer Rewards**, click **Get Payout Quote** for the newly eligible `$7.50` balance.
2. Select **Provider timeout after broadcast (`outcome_unknown` → reconcile)** under **Sandbox Settlement Scenario**, approve, and click **Authorize Payout**.
3. Notice the attempt enters `outcome_unknown`, the balance stays locked in **Payout In-Flight**, and retry is blocked until reconciled.
4. In **Operator Queue**, inspect the critical `unknown_payout_outcome` exception, then click **Run Worker & Reconciliation Tick**. The worker queries the provider, discovers the transfer actually broadcast, settles the ledger without submitting a second payout, and auto-resolves the exception.

## 4. Beneficiary Rotation Protection

1. Make `ORD-9002` fulfilled (`Merchant Campaigns` → `order.fulfilled` for `mer_sound` / `ORD-9002`) and advance clock +15d in **Operator Queue** so `$15.00` is eligible.
2. In **Customer Rewards**, click **Get Payout Quote** (binds to `v1`).
3. Before authorizing, change the Base wallet address under **Beneficiary Enrollment** (e.g. `0x2222222222222222222222222222222222222222`) and click **Save & Verify Destination** (creates `v2`, revokes `v1`).
4. Attempting to reuse the old quote fails cleanly; request a fresh quote bound to `v2`.

## 5. Post-Payout Chargeback & Provider Custody Drift

1. In **Merchant Campaigns**, send `order.chargeback` for the already-paid `ORD-9001` (`mer_trail`, `$125.00`).
2. Confirm `ORD-9001` stays `paid` (flagged `Post-payout dispute`) — irreversible stablecoin transfers are never clawed back. In **Operator Queue**, inspect the opened `post_payout_chargeback` exception showing how much the campaign `loss_reserve` covered versus uncovered shortfall.
3. Click **Simulate -$5 Custody Drift** in **Operator Queue** to see `provider_balance_mismatch` appear immediately in the discrepancy queue, then resolve with an audit note and click **Clear Drift**.
