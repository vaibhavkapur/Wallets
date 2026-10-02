#!/usr/bin/env node
/**
 * asa-mcp: Model Context Protocol server over stdio.
 *
 * Environment:
 *   ASA_AGENT_API_KEY        agent credential (agent_...). Never an owner key.
 *   ASA_CONTROL_PLANE_URL    default http://127.0.0.1:8080
 *   ASA_GATEWAY_URL          default http://127.0.0.1:8402
 */
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { AgentClient } from "@asa/sdk";
import { Tools, attemptIdSchema, createTaskSchema, paidRequestSchema, taskIdSchema } from "./tools.js";

async function main(): Promise<void> {
  const apiKey = process.env.ASA_AGENT_API_KEY;
  if (!apiKey) {
    process.stderr.write("ASA_AGENT_API_KEY is required\n");
    process.exit(2);
  }
  if (!apiKey.startsWith("agent_")) {
    process.stderr.write("ASA_AGENT_API_KEY must be an agent key; owner keys are not accepted by this adapter\n");
    process.exit(2);
  }
  const client = new AgentClient({
    controlPlaneUrl: process.env.ASA_CONTROL_PLANE_URL ?? "http://127.0.0.1:8080",
    gatewayUrl: process.env.ASA_GATEWAY_URL ?? "http://127.0.0.1:8402",
    apiKey,
  });
  const tools = new Tools(client);
  const server = new McpServer({ name: "agent-spending-accounts", version: "0.1.0" });

  server.tool("asa_create_task", "Create a spending task within the owner's policy. Returns the task id and effective budget.", createTaskSchema, (a) => tools.createTask(a));
  server.tool("asa_get_task_budget", "Show a task's remaining spendable capacity and per-request maximum.", taskIdSchema, (a) => tools.getTaskBudget(a));
  server.tool(
    "asa_paid_request",
    "Fetch an owner-approved paid resource (x402). The gateway validates the price and beneficiary against policy, pays within budget, and returns the response with a receipt. Identical calls reuse the same idempotency key and never pay twice.",
    paidRequestSchema,
    (a) => tools.paidRequest(a),
  );
  server.tool("asa_list_receipts", "List payment and service-delivery receipts for a task.", taskIdSchema, (a) => tools.listReceipts(a));
  server.tool("asa_get_payment_attempt", "Inspect a payment attempt, including pending or reconciled outcomes.", attemptIdSchema, (a) => tools.getPaymentAttempt(a));

  await server.connect(new StdioServerTransport());
}

main().catch((e) => {
  process.stderr.write(`asa-mcp: ${e instanceof Error ? e.message : String(e)}\n`);
  process.exit(1);
});
