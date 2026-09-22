import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
const port = Number(process.env.PGBASE_EXAMPLE_PORT ?? 5173);
const routes = new Map([
  ["/", [new URL("./index.html", import.meta.url), "text/html"]],
  ["/app.js", [new URL("./app.js", import.meta.url), "text/javascript"]],
]);
for (const module of ["admin", "auth-store", "index", "realtime", "response", "sse", "state-storage", "types"])
  routes.set(`/sdk/${module}.js`, [new URL(`../../sdk/typescript/dist/${module}.js`, import.meta.url), "text/javascript"]);
const server = createServer(async (request, response) => {
  const route = routes.get(request.url);
  if (request.method !== "GET" || !route) { response.writeHead(404).end(); return; }
  try { const content = await readFile(route[0]); response.writeHead(200, { "Content-Type": route[1], "Cache-Control": "no-store" }); response.end(content); }
  catch { response.writeHead(503).end("Build the local SDK first."); }
});
server.listen(port, "127.0.0.1", () => {
  const address = server.address();
  console.log(`Notes example: http://127.0.0.1:${typeof address === "object" && address !== null ? address.port : port}`);
});
