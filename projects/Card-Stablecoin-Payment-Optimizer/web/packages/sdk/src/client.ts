import type { components, paths } from "./generated/api";
import type { EvaluationResponse } from "./generated/engine-contract";

export type Schemas = components["schemas"];
export type Evaluation = Omit<Schemas["Evaluation"], "result"> & { result: EvaluationResponse };
export type Handoff = Schemas["Handoff"];
export type HandoffInput = Schemas["HandoffInput"];
export type Approval = Schemas["Approval"];
export type OutcomeInput = Schemas["OutcomeInput"];
export type ObservedOutcome = Schemas["Outcome"];
export type CustomerPreferences = Schemas["CustomerPreferences"];
export type PreferencesView = Schemas["PreferencesView"];
export type BasketInfo = Schemas["BasketInfo"];
export type EvaluateInput = Schemas["EvaluateInput"];
export type Summary = Schemas["Summary"];
export type Meta = paths["/v1/meta"]["get"]["responses"]["200"]["content"]["application/json"];

/** A non-2xx answer with the API's stable error code. */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ClientOptions {
  baseUrl: string;
  /** Bearer API key. Omit when a proxy injects it. */
  apiKey?: string;
  fetch?: typeof fetch;
}

export interface WriteOptions {
  idempotencyKey?: string;
}

/** Thin typed client over fetch. It performs no financial arithmetic. */
export class OptimizerClient {
  private readonly baseUrl: string;
  private readonly apiKey: string | undefined;
  private readonly fetchImpl: typeof fetch;

  constructor(options: ClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.apiKey = options.apiKey;
    this.fetchImpl = options.fetch ?? fetch;
  }

  private async request<T>(method: string, path: string, body?: unknown, options: WriteOptions = {}): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.apiKey) headers["Authorization"] = `Bearer ${this.apiKey}`;
    if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
    const response = await this.fetchImpl(this.baseUrl + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await response.text();
    // Amount strings are preserved: JSON.parse never touches a quoted string.
    const data: unknown = text ? JSON.parse(text) : null;
    if (!response.ok) {
      const err = (data as { error?: { code?: string; message?: string } } | null)?.error;
      throw new ApiError(response.status, err?.code ?? "http_error", err?.message ?? `HTTP ${response.status}`);
    }
    return data as T;
  }

  meta(): Promise<Meta> {
    return this.request("GET", "/v1/meta");
  }

  async catalog(): Promise<BasketInfo[]> {
    const data = await this.request<{ baskets: BasketInfo[] }>("GET", "/v1/catalog/baskets");
    return data.baskets;
  }

  evaluate(input: EvaluateInput, options?: WriteOptions): Promise<Evaluation> {
    return this.request("POST", "/v1/evaluations", input, options);
  }

  getEvaluation(id: string): Promise<Evaluation> {
    return this.request("GET", `/v1/evaluations/${encodeURIComponent(id)}`);
  }

  refresh(id: string, options?: WriteOptions): Promise<Evaluation> {
    return this.request("POST", `/v1/evaluations/${encodeURIComponent(id)}/refresh`, undefined, options);
  }

  createHandoff(evaluationId: string, input: HandoffInput, options: Required<WriteOptions>): Promise<Handoff> {
    return this.request("POST", `/v1/evaluations/${encodeURIComponent(evaluationId)}/execution-handoffs`, input, options);
  }

  getHandoff(id: string): Promise<Handoff> {
    return this.request("GET", `/v1/execution-handoffs/${encodeURIComponent(id)}`);
  }

  recordOutcome(input: OutcomeInput): Promise<ObservedOutcome> {
    return this.request("POST", "/v1/outcomes", input);
  }

  getPreferences(customerId: string): Promise<PreferencesView> {
    return this.request("GET", `/v1/customers/${encodeURIComponent(customerId)}/preferences`);
  }

  putPreferences(customerId: string, preferences: CustomerPreferences): Promise<PreferencesView> {
    return this.request("PUT", `/v1/customers/${encodeURIComponent(customerId)}/preferences`, preferences);
  }

  summary(range: { from?: string; to?: string } = {}): Promise<Summary> {
    const query = new URLSearchParams();
    if (range.from) query.set("from", range.from);
    if (range.to) query.set("to", range.to);
    const suffix = query.size ? `?${query}` : "";
    return this.request("GET", `/v1/reports/summary${suffix}`);
  }
}
