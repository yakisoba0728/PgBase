import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as api from "../api/jobs";
import { setLanguage } from "../i18n";
import { JobsView } from "./JobsView";
import { jobsQuery } from "./queries";

const job: api.ManagedJob = { id: "j1", name: "cleanup", schedule: "@every 1h", timezone: "UTC", enabled: true, registered: true, running: false, nextRunAt: "2026-11-14T00:00:00Z", attempts: 0, failed: false };

function renderJobs(client: QueryClient) {
  const router = createMemoryRouter([{ path: "*", element: <JobsView /> }], { initialEntries: ["/jobs?job=j1"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("keeps the loaded list when a refetch fails", async () => {
  const list = vi.spyOn(api, "listJobs").mockResolvedValueOnce({ items: [job], workerEnabled: true }).mockRejectedValue(new Error("offline"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderJobs(client);
  expect(await screen.findByRole("table")).toBeInTheDocument();
  await act(() => client.refetchQueries({ queryKey: jobsQuery.queryKey }));
  expect(list).toHaveBeenCalledTimes(2);
  expect(client.getQueryState(jobsQuery.queryKey)?.status).toBe("error");
  // Query results reach components on the next macrotask.
  await act(() => new Promise((done) => setTimeout(done, 10)));
  expect(screen.getByRole("table")).toBeInTheDocument();
  // The stale list stays, with a non-blocking note that the refresh failed.
  expect(screen.getByText(/Couldn’t refresh/)).toBeInTheDocument();
});

it("follows another administrator's change while the pane is untouched", async () => {
  vi.spyOn(api, "listJobs").mockResolvedValueOnce({ items: [job], workerEnabled: true }).mockResolvedValue({ items: [{ ...job, schedule: "@every 2h" }], workerEnabled: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderJobs(client);
  expect(await screen.findByDisplayValue("@every 1h")).toBeInTheDocument();
  await act(() => client.refetchQueries({ queryKey: jobsQuery.queryKey }));
  expect(await screen.findByDisplayValue("@every 2h")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});

it("says so when the linked job does not exist", async () => {
  vi.spyOn(api, "listJobs").mockResolvedValue({ items: [{ ...job, id: "j2" }], workerEnabled: true });
  renderJobs(new QueryClient({ defaultOptions: { queries: { retry: false } } }));
  expect(await screen.findByText("Not found.")).toBeInTheDocument();
});
