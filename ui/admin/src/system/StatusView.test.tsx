import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import * as system from "../api/system";
import { setLanguage } from "../i18n";
import { StatusView } from "./StatusView";

const status = {
  version: "v9.9.9", databaseReachable: true, listenerReady: true, migrationCurrent: 3, migrationLatest: 3, migrationDirty: false,
  catalogVersion: 7, sampledAt: "2026-11-14T00:00:00Z", lastSuccessAt: "2026-11-14T00:00:00Z", unavailable: [],
} as unknown as system.SystemStatus;
const health = { state: "healthy", findingCount: 0, retries: 0, findings: [], unavailable: [], sampledAt: "2026-11-14T00:00:00Z" } as unknown as system.SchemaHealth;

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("keeps the server status when only the health read fails, and retries health in place", async () => {
  vi.spyOn(system, "getSystemStatus").mockResolvedValue(status);
  const getHealth = vi.spyOn(system, "getSchemaHealth").mockRejectedValueOnce(new ApiError(500, "internal", "boom")).mockResolvedValue(health);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><StatusView /></QueryClientProvider>);

  expect(await screen.findByText("v9.9.9")).toBeInTheDocument();
  const retry = await screen.findByRole("button", { name: "Retry" });
  expect(screen.getByText("v9.9.9")).toBeInTheDocument();
  fireEvent.click(retry);
  await waitFor(() => expect(getHealth).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument());
  expect(screen.getByText("v9.9.9")).toBeInTheDocument();
});
