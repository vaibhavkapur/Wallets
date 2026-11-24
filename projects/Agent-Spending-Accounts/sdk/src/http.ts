/** Shared HTTP plumbing: auth header, JSON, error mapping, amount guards. */

export class AsaError extends Error {
  override name = "AsaError";
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly attemptId?: string,
    public readonly details?: unknown,
  ) {
    super(message);
  }

  /** True for policy denials and other deliberate refusals (4xx). */
  get isDenial(): boolean {
    return this.status === 403;
  }
}

export type FetchLike = (input: string, init: RequestInit) => Promise<Response>;

export interface HttpOptions {
  baseUrl: string;
  apiKey?: string | undefined;
  fetch?: FetchLike | undefined;
  timeoutMs?: number | undefined;
}

interface ErrorBody {
  error?: { code?: string; message?: string; details?: { attempt_id?: string } & Record<string, unknown>; attempt_id?: string };
}

export class Http {
  private readonly base: string;
  private readonly fetchImpl: FetchLike;
  constructor(private readonly opts: HttpOptions) {
    this.base = opts.baseUrl.replace(/\/+$/, "");
    this.fetchImpl = opts.fetch ?? ((input, init) => fetch(input, init));
  }

  async request<T>(method: string, path: string, body?: unknown, query?: Record<string, string | number | undefined>): Promise<T> {
    let url = this.base + path;
    if (query) {
      const qs = Object.entries(query)
        .filter(([, v]) => v !== undefined && v !== "")
        .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
        .join("&");
      if (qs) url += `?${qs}`;
    }
    const headers: Record<string, string> = { accept: "application/json" };
    if (this.opts.apiKey) headers.authorization = `Bearer ${this.opts.apiKey}`;
    if (body !== undefined) headers["content-type"] = "application/json";
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.opts.timeoutMs ?? 60_000);
    let res: Response;
    try {
      const init: RequestInit = { method, headers, signal: controller.signal };
      if (body !== undefined) init.body = JSON.stringify(body);
      res = await this.fetchImpl(url, init);
    } catch (e) {
      throw new AsaError(0, "network_error", e instanceof Error ? e.message : String(e));
    } finally {
      clearTimeout(timer);
    }
    const text = await res.text();
    let json: unknown = undefined;
    if (text) {
      try {
        json = JSON.parse(text);
      } catch {
        if (res.ok) throw new AsaError(res.status, "invalid_response", "response was not JSON");
      }
    }
    if (!res.ok) {
      const eb = (json ?? {}) as ErrorBody;
      const err = eb.error ?? {};
      throw new AsaError(
        res.status,
        err.code ?? `http_${res.status}`,
        err.message ?? text ?? res.statusText,
        err.attempt_id ?? err.details?.attempt_id,
        err.details,
      );
    }
    return json as T;
  }
}
