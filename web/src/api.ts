import type { components } from "./schema";
export type Model<K extends keyof components["schemas"]> =
  components["schemas"][K];
export type Checkout = Model<"Checkout">;
export type Order = Model<"Order">;
export type Dashboard = Model<"Dashboard">;
export async function api<T>(
  path: string,
  body?: unknown,
  key?: string,
): Promise<T> {
  const response = await fetch(`/v1${path}`, {
    method: body === undefined ? "GET" : "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(key ? { "Idempotency-Key": key } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.message || "Something went wrong. Please try again.");
  return data as T;
}
export const money = (value: string) => {
  const cents = BigInt(value);
  return `$${(cents / 100n).toLocaleString("en-US")}.${(cents % 100n).toString().padStart(2, "0")}`;
};
export function cents(input: string): string {
  if (!/^\d{1,7}(\.\d{1,2})?$/.test(input))
    throw new Error("Enter a valid amount with at most two decimal places.");
  const [d, c = ""] = input.split(".");
  return (BigInt(d) * 100n + BigInt(c.padEnd(2, "0"))).toString();
}
export const shortID = (id: string) =>
  id.split("_").at(-1)?.slice(0, 8).toUpperCase();
