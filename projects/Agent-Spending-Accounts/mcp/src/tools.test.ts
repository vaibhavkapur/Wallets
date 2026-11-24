import { describe, expect, it } from "vitest";
import { AgentClient } from "@asa/sdk";
import { Tools, defaultIdempotencyKey } from "./tools.js";

describe("MCP tools", () => {
  it("derives a stable idempotency key from task+method+url+body", () => {
    const a = { task_id: "t", url: "https://api.test/v1/x", method: "POST" as const, body: "q" };
    expect(defaultIdempotencyKey(a)).toBe(defaultIdempotencyKey({ ...a }));
    expect(defaultIdempotencyKey(a)).not.toBe(defaultIdempotencyKey({ ...a, body: "r" }));
    expect(defaultIdempotencyKey(a)).not.toBe(defaultIdempotencyKey({ ...a, task_id: "u" }));
  });

  it("returns structured denials instead of throwing, and never exposes owner operations", async () => {
    const fetchImpl = async () =>
      new Response(JSON.stringify({ error: { code: "recipient_not_approved", message: "beneficiary not approved", attempt_id: "pay_1" } }), { status: 403 });
    const client = new AgentClient({ controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "agent_k", fetch: fetchImpl });
    const tools = new Tools(client);
    const r = await tools.paidRequest({ task_id: "t", url: "https://api.test/v1/x" });
    expect(r.isError).toBe(true);
    expect(JSON.parse(r.content[0]!.text)).toMatchObject({ error: "recipient_not_approved", attempt_id: "pay_1" });
    const names = Object.getOwnPropertyNames(Tools.prototype).filter((n) => n !== "constructor");
    expect(names.sort()).toEqual(["createTask", "getPaymentAttempt", "getTaskBudget", "listReceipts", "paidRequest"]);
  });

  it("flags settled-but-undelivered results so the model does not retry blindly", async () => {
    const fetchImpl = async () =>
      new Response(
        JSON.stringify({ attempt_id: "pay_2", replay: false, payment: { status: "settled", amount: "100000" }, service: { http_status: 500, delivered: false, content_type: "", body_bytes: 0, truncated: false } }),
        { status: 200 },
      );
    const tools = new Tools(new AgentClient({ controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "agent_k", fetch: fetchImpl }));
    const r = await tools.paidRequest({ task_id: "t", url: "https://api.test/v1/x" });
    expect(JSON.parse(r.content[0]!.text).note).toMatch(/Do not retry automatically/);
  });
});
