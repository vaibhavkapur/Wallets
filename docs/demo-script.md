# Demo Script

[Documentation home](index.md)

Start `make dev` and open http://127.0.0.1:5188. All merchants, cards, prices, delivery updates, and payments are fixtures.

## Standard Purchase

1. Choose **Studio Pro**, **Midnight**, quantity **1**, a **$200.00** limit, and ZIP **94103**.
2. Click **Find my checkout**. Soundhouse quotes the lowest delivered price, **$182.94**.
3. Everyday Cash estimates **$5.48** cashback at 3%; Voyager Rewards estimates **$3.65** at 2%. Unenrolled Essential is unavailable.
4. Click **Review & approve**, inspect the purchase and checkout version, and check consent.
5. Submit **Approve & pay**. The worker creates one payment and order. Open **My orders** for the receipt.

Tax is an illustrative 8.25%, rounded half up. Cashback includes shipping and tax and rounds down to cents, valued one-to-one with USD cents. Estimates are unverified; commissions do not affect ranking.

## Alternative Outcomes

Start a new checkout for each scenario and choose it under **Sandbox scenario** before approval:

- **Issuer authentication:** waits for the customer. Complete verification to succeed or decline to release the reservation.
- **Timeout after payment:** charges once but loses the first response. The UI retains the reservation until lookup confirms the outcome, normally after 12–15 seconds.
- **Payment declined:** confirms failure, creates no order, and releases the reservation.
- **Order fails after payment:** successful charge with failed fulfillment. Open the receipt to request a refund.
- **Price changes before payment** or **Item goes out of stock:** rejects execution without a charge or reservation. Create and review a fresh checkout.

Merchant, card, or scenario changes invalidate prior approval. Quotes last 15 minutes and approvals five. Expired quotes require a new checkout.

## Refund and Cancellation

Open a receipt, enter **25.00**, and request a partial refund. Confirmation credits the budget and marks the payment partially refunded.

For an unshipped order, **Cancel & refund remaining** returns the balance to the original fixture card and marks fulfillment canceled after confirmation. Pending requests count toward the refund cap, preventing over-refunds.

## Operator Evidence

Open **Operator console** with `local-operator` when using `make dev`. Inspect budget, reservations, spending, jobs, attempts, and ledger entries. Pause purchases, verify new approvals are blocked, then resume.

Operator receipts expose **Sandbox: mark shipped** and **Sandbox: mark delivered** for eligible orders. Delivery starts the 30-day return window. These simulate merchant events, not messages to real retailers.

**Activity** traces consent, provider outcomes, refunds, and fulfillment. **My cards** enrollment only enables a predefined reference and asks for no card number.
