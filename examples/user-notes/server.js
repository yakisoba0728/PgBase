import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
const routes = new Map([
  ["/", [new URL("./index.html", import.meta.url), "text/html"]],
  ["/app.js", [new URL("./app.js", import.meta.url), "text/javascript"]],
  ["/sdk.js", [new URL("../../sdk/typescript/dist/index.js", import.meta.url), "text/javascript"]],
]);
createServer(async (request, response) => {
  const route = routes.get(request.url);
  if (request.method !== "GET" || !route) { response.writeHead(404).end(); return; }
  try { const content = await readFile(route[0]); response.writeHead(200, { "Content-Type": route[1], "Cache-Control": "no-store" }); response.end(content); }
  catch { response.writeHead(503).end("Build the local SDK first."); }
}).listen(5173, "127.0.0.1", () => console.log("Notes example: http://127.0.0.1:5173"));
