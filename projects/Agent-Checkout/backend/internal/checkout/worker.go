package checkout

import (
	"context"
	"fmt"
	"time"
)

// Work claims a durable outbox job, commits the lease, then calls the adapter outside
// the transaction. Provider idempotency uses the persisted payment/refund ID.
func (s *Service) Work(ctx context.Context, t string) (bool, error) {
	var claimed Job
	e := s.Store.Update(ctx, t, func(st *State) error {
		now := s.Now()
		for _, j := range st.Jobs {
			if j.Status == "done" || now.Before(j.NextAt) || j.Status == "running" && now.Before(j.LeaseUntil) {
				continue
			}
			claimed = j
			claimed.Status = "running"
			claimed.Attempts++
			claimed.LeaseUntil = now.Add(30 * time.Second)
			st.Jobs[j.ID] = claimed
			break
		}
		return nil
	})
	if e != nil || claimed.ID == "" {
		return false, e
	}
	st, e := s.Store.View(ctx, t)
	if e != nil {
		return true, e
	}
	switch claimed.Kind {
	case "pay", "reconcile":
		c, ok := st.Checkouts[claimed.EntityID]
		if !ok {
			e = fmt.Errorf("checkout missing")
			break
		}
		a := st.Attempts[c.AttemptID]
		if a.Status == "succeeded" || a.Status == "failed" {
			break
		}
		var result ProviderResult
		if claimed.Kind == "reconcile" {
			result, e = s.Provider.Lookup(ctx, t, a.ID)
		} else {
			result, e = s.Provider.Pay(ctx, t, c, a)
		}
		if e != nil {
			markErr := s.Store.Update(ctx, t, func(st *State) error {
				a := st.Attempts[c.AttemptID]
				if a.Status == "succeeded" || a.Status == "failed" {
					return nil
				}
				a.Status = "outcome_unknown"
				st.Attempts[a.ID] = a
				st.event(c.ID, "payment.outcome_unknown", "Provider outcome uncertain; reservation retained until lookup confirms outcome")
				st.enqueue("reconcile", c.ID)
				for id, j := range st.Jobs {
					if j.Kind == "reconcile" && j.EntityID == c.ID && j.Status == "pending" {
						j.NextAt = s.Now().Add(12 * time.Second)
						st.Jobs[id] = j
					}
				}
				return nil
			})
			if markErr != nil {
				return true, markErr
			}
			if claimed.Kind == "pay" {
				e = nil
			}
		} else {
			e = s.applyPayment(ctx, t, c.ID, result)
			if e == nil && (result.Status == "pending" || result.Status == "outcome_unknown") {
				e = ErrUnknown
			}
		}
	case "refund":
		r, ok := st.Refunds[claimed.EntityID]
		if !ok {
			e = fmt.Errorf("refund missing")
			break
		}
		var result ProviderResult
		result, e = s.Provider.Refund(ctx, t, r)
		if e == nil {
			if result.Status != "succeeded" || result.Amount != r.Amount {
				e = ErrUnknown
				break
			}
			e = s.Store.Update(ctx, t, func(st *State) error {
				ref := st.Refunds[r.ID]
				if ref.Status == "succeeded" {
					return nil
				}
				o := st.Orders[ref.OrderID]
				ref.Status = "succeeded"
				o.Refunded += ref.Amount
				st.Spent -= ref.Amount
				o.PaymentStatus = "partially_refunded"
				if o.Refunded == o.Total {
					o.PaymentStatus = "refunded"
					if o.FulfillmentStatus == "cancellation_requested" {
						o.FulfillmentStatus = "canceled"
					}
				}
				st.Orders[o.ID] = o
				st.Refunds[ref.ID] = ref
				st.entry(ref.ID, "refund", ref.Amount)
				st.event(o.ID, "refund.succeeded", "Provider confirmed refund; budget credited")
				return nil
			})
		}
	default:
		e = fmt.Errorf("unknown job type")
	}
	finishErr := s.Store.Update(ctx, t, func(st *State) error {
		j := st.Jobs[claimed.ID]
		if j.LeaseUntil != claimed.LeaseUntil {
			return nil
		}
		if e == nil {
			j.Status = "done"
		} else {
			j.Status = "pending"
			delay := time.Duration(min(j.Attempts, 6)) * 5 * time.Second
			j.NextAt = s.Now().Add(delay)
			st.event(j.EntityID, "worker.retry_scheduled", "Adapter lookup will retry; no reservation released")
		}
		st.Jobs[j.ID] = j
		return nil
	})
	if finishErr != nil {
		return true, finishErr
	}
	return true, e
}
func (s *Service) applyPayment(ctx context.Context, t, cid string, r ProviderResult) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		a := st.Attempts[c.AttemptID]
		if a.Status == "succeeded" || a.Status == "failed" {
			return nil
		}
		if r.Amount != a.Amount {
			return conflict("Provider amount does not match the approved total.")
		}
		switch r.Status {
		case "requires_customer_action":
			a.Status = r.Status
			st.Attempts[a.ID] = a
			st.event(c.ID, "payment.requires_customer_action", "Issuer verification required from the customer")
			return nil
		case "pending", "outcome_unknown":
			a.Status = r.Status
			st.Attempts[a.ID] = a
			st.enqueue("reconcile", c.ID)
			return nil
		case "failed", "succeeded":
		default:
			return ErrUnknown
		}
		a.Status = r.Status
		if a.Reserved {
			st.Reserved -= a.Amount
			a.Reserved = false
			if r.Status == "succeeded" {
				st.Spent += a.Amount
				st.entry(a.ID, "capture", a.Amount)
			} else {
				st.entry(a.ID, "release", a.Amount)
			}
		}
		if r.Status == "failed" {
			c.State = "canceled"
			st.event(c.ID, "payment.failed", "Provider confirmed no successful payment; reservation released")
		} else {
			c.State = "completed"
			var product Product
			for _, p := range Catalog() {
				if p.ID == c.Intent.ProductID {
					product = p
				}
			}
			var selected Ranking
			for _, rank := range c.Rankings {
				if rank.Card.ID == c.CardID {
					selected = rank
				}
			}
			o := Order{ID: id("order"), CheckoutID: c.ID, Product: product.Name, Variant: c.Intent.Variant, Quantity: c.Intent.Quantity, Merchant: c.Quote.Merchant, Total: a.Amount, Reward: selected.Reward, PaymentStatus: "succeeded", FulfillmentStatus: r.OrderStatus, CardName: selected.Card.Name, CardLast4: selected.Card.Last4, Delivery: c.Quote.Delivery, CreatedAt: s.Now()}
			if o.FulfillmentStatus == "" {
				o.FulfillmentStatus = "pending"
			}
			st.Orders[o.ID] = o
			c.OrderID = o.ID
			st.event(c.ID, "payment.succeeded", "Provider confirmed payment; order status recorded separately")
			if r.OrderStatus == "failed" {
				st.event(o.ID, "order.needs_support", "Payment succeeded but merchant order failed; request refund")
			}
		}
		st.Attempts[a.ID] = a
		st.Checkouts[cid] = c
		return nil
	})
}
func (s *Service) RunWorker(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tenants, e := s.Store.Tenants(ctx)
			if e != nil {
				onError(e)
				continue
			}
			for _, t := range tenants {
				for i := 0; i < 20; i++ {
					worked, e := s.Work(ctx, t)
					if e != nil {
						onError(e)
						break
					}
					if !worked {
						break
					}
				}
			}
		}
	}

}
