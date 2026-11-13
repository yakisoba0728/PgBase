import { afterEach, expect, it, vi } from "vitest";
import { getSession, login } from "./session";
import { request } from "./http";
afterEach(() => vi.unstubAllGlobals());
it("does not replace a new login's CSRF token with a delayed session read", async () => {
  let answer!: (value: Response) => void;
  const fetchMock = vi.fn().mockImplementationOnce(() => new Promise<Response>((resolve) => { answer = resolve; }))
    .mockResolvedValueOnce(new Response('{"csrfToken":"new"}'))
    .mockResolvedValueOnce(new Response('{}'));
  vi.stubGlobal("fetch", fetchMock);
  const old = getSession();
  await login({ email: "b@example.test", password: "password" });
  answer(new Response('{"csrfToken":"old"}'));
  await old;
  await request("POST", "/write");
  expect(new Headers(fetchMock.mock.calls[2][1].headers).get("X-PgBase-CSRF")).toBe("new");
});
