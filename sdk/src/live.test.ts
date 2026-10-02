/**
 * Live contract test against a running stack (scripts/dev-up.sh).
 * Enabled with ASA_LIVE=1. Verifies the SDK types line up with the real
 * control plane and gateway responses.
 */
import { describe, expect, it } from "vitest";
import { AgentClient, AsaError, OwnerClient, createOrganization, amount, formatUnits } from "./index.js";

const live = process.env.ASA_LIVE === "1";
const CP = process.env.ASA_CONTROL_PLANE_URL ?? "http://127.0.0.1:8080";
const GW = process.env.ASA_GATEWAY_URL ?? "http://127.0.0.1:8402";
const RES = process.env.ASA_RESOURCE_URL ?? "http://127.0.0.1:9402";
const PAY_TO = "0x209693Bc6afc0C5328bA36FaF03C514EF312287C";
const ASSET = "0x036CbD53842c5426634e7929541eC2318f3dCF7e";

describe.skipIf(!live)("live stack", () => {
  it("provisions, funds, pays, and reads receipts end to end", async () => {
    const { owner_api_key } = await createOrganization(CP, "SDK Live Test");
    const owner = new OwnerClient({ controlPlaneUrl: CP, apiKey: owner_api_key });
    const { agent, agent_api_key } = await owner.createAgent("sdk-agent");
    await owner.setPolicy(agent.id, {
      asset: { asset: ASSET, network: "base-sepolia", decimals: 6, scheme: "exact" },
      per_request_max: amount("500000"), per_task_max: amount("1000000"), agent_daily_max: amount("5000000"), org_daily_max: amount("5000000"), fee_allowance: amount("0"),
      endpoints: [{ url_prefix: `${RES}/v1`, methods: ["GET", "POST"], recipients: [PAY_TO] }],
      grant_ttl_seconds: 60, max_task_seconds: 3600,
    });
    await owner.deposit({ asset: ASSET, network: "base-sepolia", amount: amount("2000000"), reference: "sdk" });

    const a = new AgentClient({ controlPlaneUrl: CP, gatewayUrl: GW, apiKey: agent_api_key });
    const task = await a.createTask({ description: "sdk live", budget: amount("1000000") });
    const { data, response } = await a.paidJson<{ city: string; paid: boolean }>(task.id, { idempotencyKey: `sdk-${Date.now()}`, url: `${RES}/v1/weather?city=Oslo` });
    expect(response.payment.status).toBe("settled");
    expect(response.service?.delivered).toBe(true);
    expect(data?.paid).toBe(true);
    expect(formatUnits(response.payment.amount!, 6)).toBe("0.1");

    const budget = await a.getTaskBudget(task.id);
    expect(budget.committed).toBe("100000");
    expect((await a.listTaskReceipts(task.id)).length).toBe(1);
    expect((await owner.listBalances())[0]?.available).toBe("1900000");

    const denied = await a.paidRequest(task.id, { idempotencyKey: "x", url: `${RES}/v1/weather?mode=changed-beneficiary` }).catch((e: unknown) => e);
    expect(denied).toBeInstanceOf(AsaError);
    expect((denied as AsaError).code).toBe("challenge_rejected");
  }, 60_000);
});
