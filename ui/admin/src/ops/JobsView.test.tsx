import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as api from "../api/jobs";
import { advanceSession, ApiError } from "../api/http";
import * as notifications from "../components/Toast";
import * as operations from "../app/operations";
import { setLanguage } from "../i18n";
import { JobsView } from "./JobsView";
import { jobsQuery } from "./queries";

const job: api.ManagedJob = { id: "j1", name: "cleanup", schedule: "@every 1h", timezone: "UTC", enabled: true, registered: true, running: false, nextRunAt: "2026-11-14T00:00:00Z", attempts: 0, failed: false };

function renderJobs(client: QueryClient) {
  const router = createMemoryRouter([{ path: "*", element: <JobsView /> }], { initialEntries: ["/jobs?job=j1"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); operations.forgetOperations(); vi.restoreAllMocks(); });

it.each(["same", "replacement"])("publishes a delayed job failure only into the %s session", async (session) => {
  vi.spyOn(api, "listJobs").mockResolvedValue({ items: [job], workerEnabled: true });
  let fail!: (error: Error) => void;
  const run = vi.spyOn(api, "runJob").mockImplementation(() => new Promise((_resolve, reject) => { fail = reject; }));
  const notify = vi.spyOn(notifications, "toast").mockImplementation(() => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderJobs(client);
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
  await waitFor(() => expect(run).toHaveBeenCalledTimes(1));
  const invalidate = vi.spyOn(client, "invalidateQueries");
  if (session === "replacement") advanceSession();
  fail(new ApiError(403, session === "replacement" ? "session_changed" : "permission_denied", "Previous job request failed"));
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(notify).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  expect(invalidate).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  cleanup(); client.clear();
});

it.each(["run", "save"] as const)("does not publish an obsolete job %s receipt into a replacement session", async (operation) => {
  vi.spyOn(api, "listJobs").mockResolvedValue({ items: [job], workerEnabled: true });
  let finish!: () => void;
  const pending = new Promise<void>((resolve) => { finish = resolve; });
  const call = operation === "run" ? vi.spyOn(api, "runJob").mockReturnValue(pending) : vi.spyOn(api, "updateJob").mockReturnValue(pending);
  const notify = vi.spyOn(notifications, "toast").mockImplementation(() => {});
  const track = vi.spyOn(operations, "trackOperation");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderJobs(client);
  if (operation === "save") fireEvent.change(await screen.findByDisplayValue("@every 1h"), { target: { value: "@every 2h" } });
  fireEvent.click(await screen.findByRole("button", { name: operation === "run" ? "Run now" : "Save" }));
  await waitFor(() => expect(call).toHaveBeenCalledTimes(1));
  const invalidate = vi.spyOn(client, "invalidateQueries");
  advanceSession();
  finish();
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(notify).not.toHaveBeenCalled();
  expect(track).not.toHaveBeenCalled();
  expect(invalidate).not.toHaveBeenCalled();
  cleanup(); client.clear();
});

it("does not issue a queued job run after its click session changes", async () => {
  vi.spyOn(api, "listJobs").mockResolvedValue({ items: [job], workerEnabled: true });
  const run = vi.spyOn(api, "runJob").mockResolvedValue(undefined);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { onMutate: () => advanceSession() } } });
  renderJobs(client);
  fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(run).not.toHaveBeenCalled();
  cleanup(); client.clear();
});

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
