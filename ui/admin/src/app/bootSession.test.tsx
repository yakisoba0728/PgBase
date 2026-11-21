import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { getSession } from "../api/session";
import { RequireAuth } from "./guard";
import { queryClient, sessionQuery, startSessionCheck } from "./queries";

vi.mock("../api/session", async (original) => ({ ...(await original<typeof import("../api/session")>()), getSession: vi.fn() }));
vi.mock("../api/system", async (original) => ({ ...(await original<typeof import("../api/system")>()), getSetupStatus: vi.fn(async () => ({ setupRequired: false })) }));

const guarded = () => (
  <QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={["/collections"]}>
      <Routes>
        <Route path="/login" element={<p>login screen</p>} />
        <Route path="/collections" element={<RequireAuth>shell</RequireAuth>} />
      </Routes>
    </MemoryRouter>
  </QueryClientProvider>
);

it("uses the boot session check's answer for the guard's first mount and asks again on a later one", async () => {
  vi.mocked(getSession).mockRejectedValue(new ApiError(401, "unauthorized", "no session"));
  startSessionCheck();
  // The 401 arrives before the first render (which waits for the dictionary in main.tsx).
  await waitFor(() => expect(queryClient.getQueryState(sessionQuery.queryKey)?.status).toBe("error"));
  const first = render(guarded());
  expect(await screen.findByText("login screen")).toBeInTheDocument();
  expect(getSession).toHaveBeenCalledTimes(1);
  first.unmount();

  render(guarded());
  expect(await screen.findByText("login screen")).toBeInTheDocument();
  expect(getSession).toHaveBeenCalledTimes(2);
});
