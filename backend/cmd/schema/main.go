// Generate the versioned public contract from the same types used by the API.
package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"time"
	"unowallet/checkout/internal/checkout"
)

var schemas = map[string]any{}

func schema(t reflect.Type) map[string]any {
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeOf(checkout.Money(0)) {
		return map[string]any{"type": "string", "pattern": "^[0-9]+$", "description": "USD cents as a decimal string. Never convert to floating point."}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schema(t.Elem())}
	case reflect.Struct:
		name := t.Name()
		if _, ok := schemas[name]; !ok {
			schemas[name] = nil
			props := map[string]any{}
			required := []string{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				key := strings.Split(f.Tag.Get("json"), ",")[0]
				if key == "" || key == "-" {
					continue
				}
				props[key] = schema(f.Type)
				required = append(required, key)
			}
			schemas[name] = map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
		}
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	panic(t.String())
}
func main() {
	for _, v := range []any{checkout.Dashboard{}, checkout.IntentInput{}, checkout.Intent{}, checkout.SelectionInput{}, checkout.Checkout{}, checkout.Consent{}, checkout.ExecuteInput{}, checkout.Order{}, checkout.Refund{}} {
		schema(reflect.TypeOf(v))
	}
	paths := map[string]any{}
	for _, endpoint := range []struct{ Path, Method, Input, Output, Code string }{{"/v1/dashboard", "get", "", "Dashboard", "200"}, {"/v1/purchase-intents", "post", "IntentInput", "Intent", "201"}, {"/v1/purchase-intents/{id}/quotes", "post", "", "Checkout", "201"}, {"/v1/checkouts/{id}", "get", "", "Checkout", "200"}, {"/v1/checkouts/{id}/selection", "post", "SelectionInput", "Checkout", "200"}, {"/v1/checkouts/{id}/approval", "post", "ApprovalInput", "Consent", "201"}, {"/v1/checkouts/{id}/execute", "post", "ExecuteInput", "Checkout", "202"}, {"/v1/orders/{id}", "get", "", "Order", "200"}, {"/v1/orders/{id}/refund-requests", "post", "RefundInput", "Refund", "202"}, {"/v1/orders/{id}/cancel", "post", "RefundInput", "Refund", "202"}, {"/v1/checkouts/{id}/cancel", "post", "", "ActionResult", "200"}, {"/v1/checkouts/{id}/challenge", "post", "ChallengeInput", "ActionResult", "200"}, {"/v1/checkouts/{id}/reconcile", "post", "", "ActionResult", "202"}, {"/v1/cards/{id}/enroll", "post", "", "ActionResult", "200"}, {"/v1/operator/pause", "post", "PauseInput", "ActionResult", "200"}, {"/v1/operator/orders/{id}/advance", "post", "", "ActionResult", "200"}, {"/v1/session", "post", "SessionInput", "Session", "200"}} {
		ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
		op := map[string]any{"responses": map[string]any{endpoint.Code: map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": ref(endpoint.Output)}}}, "default": map[string]any{"description": "Application error", "content": map[string]any{"application/json": map[string]any{"schema": ref("Error")}}}}}
		params := []any{}
		if strings.Contains(endpoint.Path, "{id}") {
			params = append(params, map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
		}
		if strings.HasSuffix(endpoint.Path, "/execute") || strings.HasSuffix(endpoint.Path, "/refund-requests") || strings.HasPrefix(endpoint.Path, "/v1/orders/") && strings.HasSuffix(endpoint.Path, "/cancel") {
			params = append(params, map[string]any{"name": "Idempotency-Key", "in": "header", "required": true, "schema": map[string]any{"type": "string", "minLength": 8, "maxLength": 128}})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if endpoint.Input != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref(endpoint.Input)}}}
		}
		if endpoint.Path == "/v1/session" {
			op["security"] = []any{}
		}
		paths[endpoint.Path] = map[string]any{endpoint.Method: op}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	str := map[string]string{"type": "string"}
	boolean := map[string]string{"type": "boolean"}
	schemas["ApprovalInput"] = obj(map[string]any{"version": map[string]string{"type": "integer"}}, "version")
	schemas["RefundInput"] = obj(map[string]any{"amount": schema(reflect.TypeOf(checkout.Money(0))), "reason": str}, "amount", "reason")
	schemas["ChallengeInput"] = obj(map[string]any{"success": boolean}, "success")
	schemas["PauseInput"] = obj(map[string]any{"paused": boolean}, "paused")
	schemas["SessionInput"] = obj(map[string]any{"role": str, "token": str})
	schemas["Session"] = obj(map[string]any{"mode": str, "role": str, "name": str}, "mode", "role", "name")
	schemas["ActionResult"] = obj(map[string]any{"ok": boolean, "queued": boolean, "enrolled": boolean, "paused": boolean})
	schemas["Error"] = obj(map[string]any{"error": str, "message": str}, "error", "message")
	doc := map[string]any{"openapi": "3.1.0", "info": map[string]string{"title": "Uno Agent Checkout", "version": "1.0.0", "description": "Sandbox-only application API. The provider webhook uses timestamped HMAC; see README."}, "paths": paths, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"session": map[string]string{"type": "apiKey", "in": "cookie", "name": "uno_session"}}}, "security": []any{map[string]any{"session": []string{}}}}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e := enc.Encode(doc); e != nil {
		panic(e)
	}
}
