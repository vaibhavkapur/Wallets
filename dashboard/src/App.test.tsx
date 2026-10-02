import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { App } from "./App";
import { fmtAmount, toBaseUnits } from "./format";

describe("formatting", () => {
  it("never loses precision", () => {
    expect(fmtAmount("1250000")).toBe("1.25 USDC");
    expect(fmtAmount("123456789012345678901234567")).toBe("123456789012345678901.234567 USDC");
    expect(toBaseUnits("12.5", 6).amount).toBe("12500000");
    expect(toBaseUnits("12.5000001", 6).error).toMatch(/decimal places/);
    expect(toBaseUnits("abc", 6).error).toBeTruthy();
  });
});

describe("sign-in", () => {
  it("refuses agent keys for the owner console", () => {
    sessionStorage.clear();
    render(<App />);
    fireEvent.change(screen.getByLabelText(/owner api key/i), { target: { value: "agent_123" } });
    fireEvent.click(screen.getByText(/open console/i));
    expect(screen.getByText(/use an owner api key/i)).toBeTruthy();
  });
});
