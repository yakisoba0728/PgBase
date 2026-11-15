import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";

const root = new URL("../../../../examples/javascript/public/", import.meta.url);
const html = await readFile(new URL("index.html", root), "utf8");
const script = await readFile(new URL("app.js", root), "utf8");

test("a shared example link cannot choose where entered credentials are sent", async ({ page }) => {
  const sent: { url: string; body: string | null }[] = [];
  // All network is intercepted: this is a browser contract, not a live server test.
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const cors = { "Access-Control-Allow-Origin": "https://pgbase.test" };
    // A hostile API can opt into CORS. The destination boundary must not rely
    // on a refused preflight preventing the credential-bearing POST.
    if (request.method() === "OPTIONS") {
      await route.fulfill({ status: 204, headers: {
        ...cors, "Access-Control-Allow-Methods": "POST",
        "Access-Control-Allow-Headers": "content-type,authorization",
      } });
    } else if (request.method() === "POST") {
      sent.push({ url: url.href, body: request.postData() });
      await route.fulfill({ status: 401, headers: cors, contentType: "application/json", body: '{"error":{"code":"unauthorized"}}' });
    } else if (url.pathname === "/app.js") {
      await route.fulfill({ contentType: "application/javascript", body: script });
    } else if (url.origin === "https://pgbase.test" && url.pathname === "/") {
      await route.fulfill({ contentType: "text/html", body: html });
    } else {
      await route.abort();
    }
  });
  await page.goto("https://pgbase.test/?api=https%3A%2F%2Fattacker.test");
  await page.getByLabel("Email for an existing disposable user").fill("member@example.test");
  await page.getByLabel("Password", { exact: true }).fill("disposable-test-password");
  await page.getByRole("button", { name: "Run acceptance" }).click();
  await expect(page.getByRole("status")).toContainText("FAIL");
  expect(sent).toEqual([{
    url: "https://pgbase.test/api/v1/auth/login",
    body: JSON.stringify({ email: "member@example.test", password: "disposable-test-password" }),
  }]);
  await expect(page.getByRole("button", { name: "Run acceptance" })).toBeEnabled();
});
