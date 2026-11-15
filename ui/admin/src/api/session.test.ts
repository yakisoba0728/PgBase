import { afterEach, expect, it, vi } from "vitest";
import { getSession, login } from "./session";
import { clearCsrfToken, request } from "./http";
afterEach(() => { vi.unstubAllGlobals(); clearCsrfToken(); });
it("rejects a delayed session read and keeps the new login's CSRF token", async () => {
  let answer!: (value: Response) => void;
  const fetchMock = vi.fn().mockImplementationOnce(() => new Promise<Response>((resolve) => { answer = resolve; }))
    .mockResolvedValueOnce(new Response('{"csrfToken":"new"}'))
    .mockResolvedValueOnce(new Response('{}'));
  vi.stubGlobal("fetch", fetchMock);
  const old = getSession();
  await login({ email: "b@example.test", password: "password" });
  answer(new Response('{"csrfToken":"old"}'));
  await expect(old).rejects.toMatchObject({ code: "session_changed" });
  await request("POST", "/write");
  expect(new Headers(fetchMock.mock.calls[2][1].headers).get("X-PgBase-CSRF")).toBe("new");
});
