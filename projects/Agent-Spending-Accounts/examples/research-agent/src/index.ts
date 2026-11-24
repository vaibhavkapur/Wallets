/**
 * Example agent workflow.
 *
 * The OWNER side (normally a dashboard or ops script) provisions an agent,
 * approves exactly one data provider and its payment address, and funds the
 * account. The AGENT side then runs a small research task: it buys several
 * paid results, stays within its task budget, handles a denial gracefully,
 * and prints the receipts and cost per task.
 *
 * Run against the local stack:  scripts/dev-up.sh  then  npm start -w examples/research-agent
 */
import { AgentClient, AsaError, OwnerClient, amount, createOrganization, formatUnits } from "@asa/sdk";

const CP = process.env.ASA_CONTROL_PLANE_URL ?? "http://127.0.0.1:8080";
const GW = process.env.ASA_GATEWAY_URL ?? "http://127.0.0.1:8402";
const RESOURCE = process.env.ASA_RESOURCE_URL ?? "http://127.0.0.1:9402";
const PAY_TO = process.env.ASA_PAY_TO ?? "0x209693Bc6afc0C5328bA36FaF03C514EF312287C";
const USDC = { asset: "0x036CbD53842c5426634e7929541eC2318f3dCF7e", network: "base-sepolia", decimals: 6, scheme: "exact" };
const usd = (a: string) => `$${formatUnits(amount(a), USDC.decimals)}`;

async function ownerSetup() {
  // 1. Operator bootstraps an organization; the owner key must stay out of agent processes.
  const { organization, owner_api_key } = await createOrganization(CP, "Example Research Org");
  const owner = new OwnerClient({ controlPlaneUrl: CP, apiKey: owner_api_key });

  // 2. Owner creates an agent identity and an explicit spending policy:
  //    "may spend up to $0.50 per request, $1 per task, $5/day on this one data provider".
  const { agent, agent_api_key } = await owner.createAgent("research-agent");
  await owner.setPolicy(agent.id, {
    asset: USDC,
    per_request_max: amount("500000"),
    per_task_max: amount("1000000"),
    agent_daily_max: amount("5000000"),
    org_daily_max: amount("20000000"),
    fee_allowance: amount("0"),
    endpoints: [{ url_prefix: `${RESOURCE}/v1`, methods: ["GET", "POST"], recipients: [PAY_TO] }],
    max_task_seconds: 3600,
    grant_ttl_seconds: 60,
  });

  // 3. Owner funds the account (in production this follows the provider's deposit flow).
  await owner.deposit({ asset: USDC.asset, network: USDC.network, amount: amount("3000000"), reference: "example" });
  console.log(`owner: org ${organization.id} funded with ${usd("3000000")}; agent ${agent.id} limited to ${usd("1000000")}/task`);
  return { owner, agent_api_key };
}

async function agentRun(agentKey: string) {
  const agent = new AgentClient({ controlPlaneUrl: CP, gatewayUrl: GW, apiKey: agentKey });

  // The agent opens a task; the budget it asks for is capped by policy.
  const task = await agent.createTask({ description: "Compare weather in three cities", budget: amount("1000000") });
  console.log(`agent: task ${task.id} budget ${usd(task.budget)}`);

  const cities = ["Lisbon", "Nairobi", "Osaka"];
  for (const city of cities) {
    const { data, response } = await agent.paidJson<{ city: string; temperature_c: number }>(task.id, {
      idempotencyKey: `${task.id}:weather:${city}`,
      url: `${RESOURCE}/v1/weather?city=${encodeURIComponent(city)}`,
    });
    if (response.payment.status === "settled" && response.service?.delivered && data) {
      console.log(`agent: ${city}: ${data.temperature_c}°C  (paid ${usd(response.payment.amount!)}, tx ${response.payment.transaction})`);
    } else {
      console.log(`agent: ${city}: payment=${response.payment.status} delivered=${response.service?.delivered ?? false} — not retrying automatically`);
    }
  }

  // Idempotency: repeating a call reuses the receipt and does not pay again.
  const again = await agent.paidRequest(task.id, { idempotencyKey: `${task.id}:weather:Lisbon`, url: `${RESOURCE}/v1/weather?city=Lisbon` });
  console.log(`agent: repeat Lisbon -> replay=${again.replay}, status=${again.payment.status}`);

  // A resource that tries to redirect payment to another address is refused before any money moves.
  try {
    await agent.paidRequest(task.id, { idempotencyKey: `${task.id}:evil`, url: `${RESOURCE}/v1/weather?mode=changed-beneficiary` });
  } catch (e) {
    if (e instanceof AsaError) console.log(`agent: denied as expected: ${e.code} — ${e.message}`);
    else throw e;
  }

  // Summarize a paid POST, then check what is left.
  const summary = await agent.paidJson<{ summary: string }>(task.id, {
    idempotencyKey: `${task.id}:summarize`,
    method: "POST",
    url: `${RESOURCE}/v1/summarize`,
    headers: { "content-type": "text/plain" },
    body: cities.join(", "),
  });
  console.log(`agent: summary -> ${summary.data?.summary ?? "(none)"}`);

  const budget = await agent.getTaskBudget(task.id);
  console.log(`agent: task spent ${usd(budget.committed)}, remaining ${usd(budget.remaining)}, spendable=${budget.spendable}`);

  // Exceeding the task budget is denied with a stable code; nothing is held.
  try {
    for (let i = 0; i < 5; i++) {
      await agent.paidRequest(task.id, { idempotencyKey: `${task.id}:extra:${i}`, url: `${RESOURCE}/v1/weather?city=Extra${i}` });
    }
  } catch (e) {
    if (e instanceof AsaError) console.log(`agent: stopped by policy: ${e.code}`);
    else throw e;
  }
  return task.id;
}

async function main() {
  const { owner, agent_api_key } = await ownerSetup();
  const taskId = await agentRun(agent_api_key);

  // Owner-side visibility: cost per task and receipts.
  const receipts = await owner.listReceipts({ taskId });
  const spent = receipts.reduce((acc, r) => acc + BigInt(r.settled_amount), 0n);
  console.log(`owner: task ${taskId}: ${receipts.length} receipts, total ${usd(spent.toString())}`);
  for (const r of receipts) {
    console.log(`  - ${r.payment_status.padEnd(8)} ${usd(r.settled_amount).padEnd(8)} ${r.resource}  delivered=${r.service?.delivered ?? "n/a"}`);
  }
  const [bal] = await owner.listBalances();
  console.log(`owner: balance available ${usd(bal!.available)}, reserved ${usd(bal!.reserved)}`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
