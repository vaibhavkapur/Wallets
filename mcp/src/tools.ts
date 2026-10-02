/**
 * Tool definitions for the MCP adapter.
 *
 * Security posture: this process holds only an AGENT credential. The tools
 * can create tasks within the owner's policy, inspect a task's budget,
 * execute paid requests through the gateway, and read receipts. There is no
 * tool to change limits, approve beneficiaries, deposit, or withdraw: those
 * are owner-only operations and are not reachable with an agent key.
 */
import { createHash } from "node:crypto";
import { AgentClient, AsaError, amount } from "@asa/sdk";
import { z } from "zod";

export const createTaskSchema = {
  description: z.string().max(1000).optional().describe("What the task is for (shown to the account owner)"),
  budget: z.string().regex(/^(0|[1-9][0-9]*)$/).optional().describe("Requested task budget in base units as a decimal string; capped by policy"),
  duration_seconds: z.number().int().positive().optional().describe("Requested task lifetime; capped by policy"),
};

export const taskIdSchema = { task_id: z.string().min(1) };

export const paidRequestSchema = {
  task_id: z.string().min(1),
  url: z.string().url().describe("Absolute URL of an owner-approved paid resource"),
  method: z.enum(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"]).optional(),
  headers: z.record(z.string()).optional(),
  body: z.string().optional().describe("UTF-8 request body"),
  idempotency_key: z.string().min(1).max(200).optional().describe("Defaults to a hash of task+method+url+body so identical calls never pay twice"),
  max_amount: z.string().regex(/^(0|[1-9][0-9]*)$/).optional().describe("Optional ceiling in base units below the policy maximum"),
};

export const attemptIdSchema = { attempt_id: z.string().min(1) };

export type PaidRequestArgs = {
  task_id: string;
  url: string;
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";
  headers?: Record<string, string>;
  body?: string;
  idempotency_key?: string;
  max_amount?: string;
};

export function defaultIdempotencyKey(a: PaidRequestArgs): string {
  const h = createHash("sha256");
  h.update(a.task_id).update("\n").update(a.method ?? "GET").update("\n").update(a.url).update("\n").update(a.body ?? "");
  return `mcp-${h.digest("hex").slice(0, 32)}`;
}

export interface ToolResult {
  [key: string]: unknown;
  content: { type: "text"; text: string }[];
  isError?: boolean;
}

function ok(v: unknown): ToolResult {
  return { content: [{ type: "text", text: JSON.stringify(v, null, 2) }] };
}

function fail(e: unknown): ToolResult {
  if (e instanceof AsaError) {
    return { isError: true, content: [{ type: "text", text: JSON.stringify({ error: e.code, message: e.message, attempt_id: e.attemptId, status: e.status }) }] };
  }
  return { isError: true, content: [{ type: "text", text: JSON.stringify({ error: "unexpected", message: e instanceof Error ? e.message : String(e) }) }] };
}

export class Tools {
  constructor(private readonly client: AgentClient) {}

  async createTask(a: { description?: string; budget?: string; duration_seconds?: number }): Promise<ToolResult> {
    try {
      const t = await this.client.createTask({
        ...(a.description !== undefined ? { description: a.description } : {}),
        ...(a.budget !== undefined ? { budget: amount(a.budget) } : {}),
        ...(a.duration_seconds !== undefined ? { durationSeconds: a.duration_seconds } : {}),
      });
      return ok({ task_id: t.id, budget: t.budget, deadline: t.deadline, status: t.status });
    } catch (e) {
      return fail(e);
    }
  }

  async getTaskBudget(a: { task_id: string }): Promise<ToolResult> {
    try {
      return ok(await this.client.getTaskBudget(a.task_id));
    } catch (e) {
      return fail(e);
    }
  }

  async paidRequest(a: PaidRequestArgs): Promise<ToolResult> {
    try {
      const r = await this.client.paidRequest(a.task_id, {
        idempotencyKey: a.idempotency_key ?? defaultIdempotencyKey(a),
        url: a.url,
        ...(a.method ? { method: a.method } : {}),
        ...(a.headers ? { headers: a.headers } : {}),
        ...(a.body !== undefined ? { body: a.body } : {}),
        ...(a.max_amount !== undefined ? { maxAmount: amount(a.max_amount) } : {}),
      });
      // Keep the model-facing payload small and explicit about outcomes.
      return ok({
        attempt_id: r.attempt_id,
        replay: r.replay,
        payment: r.payment,
        service: r.service
          ? { http_status: r.service.http_status, delivered: r.service.delivered, content_type: r.service.content_type, truncated: r.service.truncated, body: r.service.body ?? null }
          : null,
        receipt_id: r.receipt?.id ?? null,
        note:
          r.payment.status === "settled" && r.service && !r.service.delivered
            ? "Payment settled but the resource did not deliver a usable result. Do not retry automatically; report this to the owner via receipts."
            : r.payment.status === "unknown"
              ? "Payment outcome is unknown; the reservation is held until reconciliation. Poll asa_get_payment_attempt before retrying."
              : undefined,
      });
    } catch (e) {
      return fail(e);
    }
  }

  async listReceipts(a: { task_id: string }): Promise<ToolResult> {
    try {
      return ok(await this.client.listTaskReceipts(a.task_id));
    } catch (e) {
      return fail(e);
    }
  }

  async getPaymentAttempt(a: { attempt_id: string }): Promise<ToolResult> {
    try {
      return ok(await this.client.getPaymentAttempt(a.attempt_id));
    } catch (e) {
      return fail(e);
    }
  }
}
