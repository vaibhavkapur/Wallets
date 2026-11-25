package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	spec := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Recurring Purchase Agent API",
			"version":     "1.0.0",
			"description": "Timezone-aware recurring essentials reorder workflow with immutable rule versions, fresh quotes, exact approval binding, atomic monthly budget reservations, and payment/fulfillment reconciliation.",
		},
		"paths": map[string]any{
			"/healthz": map[string]any{
				"get": map[string]any{"summary": "Service health check"},
			},
			"/v1/session": map[string]any{
				"post": map[string]any{"summary": "Open a tenant-isolated customer or operator session"},
			},
			"/v1/catalog": map[string]any{
				"get": map[string]any{"summary": "List narrow coffee and household essentials catalog and scoped payment methods"},
			},
			"/v1/workspace": map[string]any{
				"get": map[string]any{"summary": "Fetch tenant rules, occurrences, proposals, consents, budgets, notifications, and audit log"},
			},
			"/v1/recurring-rules": map[string]any{
				"post": map[string]any{"summary": "Create a recurring rule (v1) and its approval mode"},
			},
			"/v1/recurring-rules/{id}": map[string]any{
				"patch": map[string]any{"summary": "Create a new immutable version of schedule and purchase constraints"},
			},
			"/v1/recurring-rules/{id}/pause": map[string]any{
				"post": map[string]any{"summary": "Pause a rule to block future occurrences (distinct from canceling submitted orders)"},
			},
			"/v1/recurring-rules/{id}/resume": map[string]any{
				"post": map[string]any{"summary": "Resume a paused rule without generating catch-up purchases"},
			},
			"/v1/recurring-rules/{id}/cancel": map[string]any{
				"post": map[string]any{"summary": "Cancel a recurring rule permanently"},
			},
			"/v1/recurring-rules/{id}/skip-next": map[string]any{
				"post": map[string]any{"summary": "Skip one identified upcoming schedule occurrence"},
			},
			"/v1/recurring-rules/{id}/occurrences": map[string]any{
				"get": map[string]any{"summary": "Explain upcoming and past schedule occurrences for a rule"},
			},
			"/v1/recurring-rules/{id}/mandates": map[string]any{
				"post": map[string]any{"summary": "Grant a scoped C3 delegated mandate for an eligible rule"},
			},
			"/v1/consents/{id}/revoke": map[string]any{
				"post": map[string]any{"summary": "Revoke an approval consent or delegated mandate"},
			},
			"/v1/proposals/{id}/approve": map[string]any{
				"post": map[string]any{"summary": "Approve an exact current proposal bound to rule version, quote digest, and amount"},
			},
			"/v1/proposals/{id}/reject": map[string]any{
				"post": map[string]any{"summary": "Reject a pending purchase proposal without purchasing"},
			},
			"/v1/occurrences/{id}": map[string]any{
				"get": map[string]any{"summary": "Return scheduling, quote, proposal, checkout, payment, and fulfillment status"},
			},
			"/v1/occurrences/{id}/reconcile": map[string]any{
				"post": map[string]any{"summary": "Reconcile a timeout_unknown checkout without creating a duplicate charge"},
			},
			"/v1/occurrences/{id}/cancel-order": map[string]any{
				"post": map[string]any{"summary": "Request merchant cancellation of an already-submitted order"},
			},
			"/v1/webhooks/merchant": map[string]any{
				"post": map[string]any{"summary": "HMAC-verified merchant webhook for fulfillment and partial/full refund updates"},
			},
			"/v1/operator/pause": map[string]any{
				"post": map[string]any{"summary": "Operator-only product-level emergency pause control"},
			},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(spec); err != nil {
		fmt.Fprintf(os.Stderr, "encode schema: %v\n", err)
		os.Exit(1)
	}
}
