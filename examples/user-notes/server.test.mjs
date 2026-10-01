import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import test from "node:test";

test("serves the browser SDK entry point and its transitive modules", { timeout: 10_000 }, async (t) => {
  const server = spawn(process.execPath, ["server.js"], {
    cwd: import.meta.dirname,
    env: { ...process.env, PGBASE_EXAMPLE_PORT: "0" },
    stdio: ["ignore", "pipe", "inherit"],
  });
  t.after(() => server.kill());
  const port = await new Promise((resolve, reject) => {
    server.stdout.once("data", (chunk) => {
      const match = String(chunk).match(/:(\d+)\s*$/);
      if (match) resolve(Number(match[1]));
      else reject(new Error(`unexpected startup output: ${chunk}`));
    });
    server.once("exit", (code) => reject(new Error(`example server exited with ${code}`)));
  });

  const visited = new Set();
  async function fetchModule(url) {
    if (visited.has(url.href)) return;
    visited.add(url.href);
    const response = await fetch(url);
    assert.equal(response.status, 200, url.pathname);
    const source = await response.text();
    const imports = [
      ...source.matchAll(/from\s+["'](\.\/[^"']+)["']/g),
      ...source.matchAll(/import\s+["'](\.\/[^"']+)["']/g),
    ].map((match) => match[1]);
    await Promise.all(imports.map((specifier) => fetchModule(new URL(specifier, url))));
  }
  await fetchModule(new URL(`http://127.0.0.1:${port}/sdk/index.js`));
  assert.ok(visited.size > 1);
  assert.equal((await fetch(`http://127.0.0.1:${port}/sdk/index.d.ts`)).status, 404);
  assert.equal((await fetch(`http://127.0.0.1:${port}/sdk/../server.js`)).status, 404);
});
