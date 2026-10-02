import { describe, expect, it } from "vitest";
import { AgentClient, AsaError, OwnerClient, amount } from "./index.js";

interface Captured {
  url: string;
  init: RequestInit;
}

function fakeFetch(status: number, body: unknown, captured: Captured[]) {
  return async (url: string, init: RequestInit): Promise<Response> => {
    captured.push({ url, init });
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  };
}

describe("AgentClient contract", () => {
  it("refuses owner keys and sends agent credentials as bearer tokens", async () => {
    expect(() => new AgentClient({ controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "owner_x" })).toThrow();
    const captured: Captured[] = [];
    const c = new AgentClient({ controlPlaneUrl: "http://cp/", gatewayUrl: "http://gw", apiKey: "agent_k", fetch: fakeFetch(201, { id: "tsk_1" }, captured) });
    await c.createTask({ description: "d", budget: amount("5000000"), durationSeconds: 60 });
    expect(captured[0]?.url).toBe("http://cp/v1/tasks");
    expect((captured[0]?.init.headers as Record<string, string>).authorization).toBe("Bearer agent_k");
    expect(JSON.parse(captured[0]?.init.body as string)).toEqual({ description: "d", budget: "5000000", duration_seconds: 60 });
  });

  it("sends paid requests to the gateway with snake_case fields and string amounts", async () => {
    const captured: Captured[] = [];
    const resp = { attempt_id: "pay_1", replay: false, payment: { status: "settled", amount: "100000" }, service: { http_status: 200, delivered: true, content_type: "application/json", body_bytes: 2, truncated: false, body: '{"ok":true}' } };
    const c = new AgentClient({ controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "agent_k", fetch: fakeFetch(200, resp, captured) });
    const { data, response } = await c.paidJson<{ ok: boolean }>("tsk_1", { idempotencyKey: "i1", url: "https://api.test/v1/x", method: "POST", body: "hi", headers: { accept: "application/json" }, maxAmount: amount("200000") });
    expect(captured[0]?.url).toBe("http://gw/v1/tasks/tsk_1/paid-requests");
    expect(JSON.parse(captured[0]?.init.body as string)).toEqual({
      idempotency_key: "i1", method: "POST", url: "https://api.test/v1/x", headers: { accept: "application/json" }, body: "hi", max_amount: "200000",
    });
    expect(data).toEqual({ ok: true });
    expect(response.payment.amount).toBe("100000");
    expect(typeof response.payment.amount).toBe("string");
  });

  it("maps denials to AsaError with stable codes and attempt ids", async () => {
    const c = new AgentClient({
      controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "agent_k",
      fetch: fakeFetch(403, { error: { code: "task_budget_exceeded", message: "nope", attempt_id: "pay_9" } }, []),
    });
    const err = await c.paidRequest("tsk_1", { idempotencyKey: "i", url: "https://api.test/v1" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(AsaError);
    const e = err as AsaError;
    expect(e.status).toBe(403);
    expect(e.code).toBe("task_budget_exceeded");
    expect(e.attemptId).toBe("pay_9");
    expect(e.isDenial).toBe(true);
  });

  it("requires an idempotency key", async () => {
    const c = new AgentClient({ controlPlaneUrl: "http://cp", gatewayUrl: "http://gw", apiKey: "agent_k", fetch: fakeFetch(200, {}, []) });
    expect(() => c.paidRequest("t", { idempotencyKey: "", url: "https://x" })).toThrow();
  });
});

describe("OwnerClient contract", () => {
  it("validates amounts in policies and deposits before sending", async () => {
    const captured: Captured[] = [];
    const o = new OwnerClient({ controlPlaneUrl: "http://cp", apiKey: "owner_k", fetch: fakeFetch(201, { id: "pol_1" }, captured) });
    await o.setPolicy("agt_1", {
      asset: { asset: "0xA", network: "base-sepolia", decimals: 6, scheme: "exact" },
      per_request_max: amount("1"), per_task_max: amount("2"), agent_daily_max: amount("3"), org_daily_max: amount("4"), fee_allowance: amount("0"),
      endpoints: [{ url_prefix: "https://api.test/v1", recipients: ["0xB"] }],
    });
    expect(captured[0]?.url).toBe("http://cp/v1/agents/agt_1/policy");
    expect(captured[0]?.init.method).toBe("PUT");
    // A float sneaking in through a cast is rejected before any request.
    expect(() => o.deposit({ asset: "0xA", network: "n", amount: "1.5" as never })).toThrow();
    expect(captured.length).toBe(1);
  });

  it("unwraps list envelopes and passes query filters", async () => {
    const captured: Captured[] = [];
    const o = new OwnerClient({ controlPlaneUrl: "http://cp", apiKey: "owner_k", fetch: fakeFetch(200, { receipts: [{ id: "rcp_1" }] }, captured) });
    const rs = await o.listReceipts({ taskId: "tsk_1", limit: 5 });
    expect(rs).toEqual([{ id: "rcp_1" }]);
    expect(captured[0]?.url).toBe("http://cp/v1/receipts?task_id=tsk_1&limit=5");
  });
});
