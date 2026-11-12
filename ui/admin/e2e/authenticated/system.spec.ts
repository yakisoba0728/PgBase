import { expect, test } from "./fixtures";

test("reports operational diagnostics without leaking a secret", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/admin/collections");

  // Both samplers deliberately answer from their cached first run. Compose can
  // be healthy while the bounded doctor scan is still filling that cache, and
  // SystemPage fetches only once per mount. Wait on the actual precondition so
  // a retry is not what supplies a later mount after the sample completes.
  await expect.poll(async () => {
    try {
      const replies = await Promise.all([
        page.request.get("/api/admin/system"),
        page.request.get("/api/admin/system/health"),
      ]);
      if (replies.some((reply) => !reply.ok())) return false;
      const snapshots = await Promise.all(replies.map((reply) => reply.json() as Promise<{ lastSuccessAt?: unknown }>));
      return snapshots.every((snapshot) => typeof snapshot.lastSuccessAt === "string" && snapshot.lastSuccessAt !== "");
    } catch {
      return false;
    }
  }, { timeout: 65_000 }).toBe(true);

  // The address a new tab or "copy link address" would use has to be the same
  // typed route an in-place click reaches.
  const system = page.getByRole("link", { name: "System" });
  await expect(system).toHaveAttribute("href", "/admin/system");
  await system.click();

  await expect(page.getByRole("heading", { name: "System" })).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/admin/system");

  const status = page.getByRole("region", { name: "Instance status", exact: true });
  await expect(status).toBeVisible();
  for (const label of [
    "Binary version", "Database", "Migrations", "Migration history",
    "Catalog version", "Catalog listener", "Last successful sample",
  ]) {
    await expect(status.getByText(label, { exact: true })).toBeVisible();
  }
  await expect(status.getByText("Reachable")).toBeVisible();
  await expect(status.getByText("Clean")).toBeVisible();
  await expect(status.getByText("Ready")).toBeVisible();
  // A sampled reading is only trustworthy because the sample that produced it
  // is dated; an undated one would be indistinguishable from a stale cache.
  await expect(status.locator("time")).toHaveCount(1);

  // The diagnosis this backend actually ran: a freshly migrated instance has no
  // drift, and "healthy" has to be the state rather than the absence of one.
  await expect(page.getByRole("heading", { name: "Schema health" })).toBeVisible();
  await expect(page.locator("p.health-state")).toHaveAttribute("data-state", "healthy");
  await expect(page.getByText("No drift detected")).toBeVisible();
  await expect(page.getByText("No findings reported.")).toBeVisible();
  const health = page.getByRole("region", { name: "Schema health", exact: true });
  await expect(health.getByText("Last successful diagnosis", { exact: true })).toBeVisible();
  await expect(health.getByText("Retries since the last diagnosis", { exact: true })).toBeVisible();
  await expect(health.locator("time")).toHaveCount(1);

  // The endpoint is redacted server-side; the page must never render a
  // connection string, token or password even if one arrived.
  const rendered = await page.locator("body").innerText();
  expect(rendered).not.toMatch(/postgres(ql)?:\/\//);
  expect(rendered.toLowerCase()).not.toContain("password");
  expect(rendered.toLowerCase()).not.toContain("secret");
  expect(rendered).not.toContain(process.env.PGBASE_ADMIN_PASSWORD ?? "\u0000never");
});
