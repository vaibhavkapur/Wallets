import { test, expect, type Page } from "@playwright/test";

async function quote(page: Page, scenario = "success") {
  await page.goto("/");
  await page
    .getByRole("button", { name: "Find my checkout", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Your item. Three ways to get it." }),
  ).toBeVisible();
  if (scenario !== "success") {
    await page.getByText("Sandbox scenario", { exact: true }).click();
    await page.getByLabel("Next payment behavior").selectOption(scenario);
  }
  await expect(
    page.getByRole("button", { name: "Review & approve", exact: true }),
  ).toBeEnabled();
}
async function pay(page: Page) {
  await page
    .getByRole("button", { name: "Review & approve", exact: true })
    .click();
  const approve = page.getByRole("button", {
    name: "Approve & pay $182.94",
    exact: true,
  });
  await expect(approve).toBeDisabled();
  await page.getByRole("dialog").getByRole("checkbox").check();
  await approve.click();
}
test("approved purchase, partial refund and remaining cancellation", async ({
  page,
}) => {
  await quote(page);
  await pay(page);
  await page.getByRole("button", { name: "View receipt", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("$182.94");
  await page
    .getByRole("textbox", { name: "Refund amount", exact: true })
    .fill("25.00");
  await page
    .getByRole("button", { name: "Request refund", exact: true })
    .click();
  await expect(page.getByRole("status")).toContainText("Refund requested");
  await page.getByRole("button", { name: "View receipt", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("partially refunded");
  await page
    .getByRole("button", { name: "Cancel & refund remaining", exact: true })
    .click();
  await page.getByRole("button", { name: "My orders", exact: true }).click();
  await expect(page.locator(".order-row").first()).toContainText("canceled");
});
test("issuer challenge requires customer confirmation", async ({ page }) => {
  await quote(page, "challenge");
  await pay(page);
  await page
    .getByRole("button", { name: "Complete sandbox verification", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "A good purchase, approved by you." }),
  ).toBeVisible();
});
test("unknown result reconciles without another purchase", async ({ page }) => {
  await quote(page, "timeout");
  await pay(page);
  await expect(
    page.getByRole("heading", { name: "Confirming your payment." }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review & approve", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "A good purchase, approved by you." }),
  ).toBeVisible({ timeout: 25000 });
});
test("changed quote cannot charge", async ({ page }) => {
  await quote(page, "price_changed");
  await pay(page);
  await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
    "changed the price or stock",
  );
  await expect(
    page.getByRole("heading", { name: "One last look. Your call." }),
  ).toBeVisible();
});
test("decline releases its reservation", async ({ page }) => {
  await quote(page, "declined");
  await pay(page);
  await expect(
    page.getByRole("heading", { name: "Your payment was declined." }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "The provider confirmed the payment failed. Your reserved budget has been released.",
    ),
  ).toBeVisible();
});
test("operator access, pause, and resume", async ({ page }) => {
  await page.goto("/");
  await page
    .getByRole("button", { name: "Operator console", exact: true })
    .click();
  await page.getByLabel("Operator token").fill("wrong-token");
  await page.getByRole("button", { name: "Open console", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("operator access token");
  await page.getByLabel("Operator token").fill("e2e-operator");
  await page.getByRole("button", { name: "Open console", exact: true }).click();
  await page
    .getByRole("button", { name: "Pause purchases", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Resume purchases", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Resume purchases", exact: true })
    .click();
});
test("mobile checkout has named navigation and no horizontal overflow", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await quote(page);
  await expect(
    page.getByRole("button", { name: "My orders", exact: true }),
  ).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    )
    .toBe(true);
  await page
    .getByRole("button", { name: "Review & approve", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
});
