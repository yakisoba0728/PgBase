import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import * as realtime from "../api/realtime";
import { setLanguage } from "../i18n";
import { realtimeKey } from "./queries";
import { RealtimeView } from "./RealtimeView";

const event = (id: string) => ({ eventId: id, collectionName: "posts", recordId: `r-${id}`, operation: "update", recordVersion: 1, attempts: 10, createdAt: "2026-11-14T00:00:00Z", deadAt: "2026-11-14T00:00:00Z" }) as realtime.DeadRealtimeEvent;

beforeEach(() => {
  setLanguage("en");
  vi.spyOn(realtime, "getRealtimeStatus").mockResolvedValue({ pending: 0, dead: 3, delivered: 0, cursorFloor: 0 } as realtime.RealtimeStatus);
  vi.spyOn(realtime, "listDeadRealtimeEvents").mockResolvedValue({ items: [event("e1"), event("e2"), event("e3")] });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("retries the selected events, summarises a partial failure and keeps the failed one selected", async () => {
  const retry = vi.spyOn(realtime, "retryRealtimeEvent").mockImplementation(async (id) => {
    if (id === "e2") throw new ApiError(500, "internal", "boom");
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  render(<QueryClientProvider client={client}><RealtimeView /></QueryClientProvider>);

  fireEvent.click(await screen.findByRole("checkbox", { name: "Select e1" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Select e2" }));
  const all = screen.getByRole("checkbox", { name: "Select all loaded events" }) as HTMLInputElement;
  expect(all.indeterminate).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Retry selected (2)" }));

  expect(await screen.findByText("1 of 2 requested; 1 failed.")).toBeInTheDocument();
  expect(retry).toHaveBeenCalledTimes(2);
  expect(retry).not.toHaveBeenCalledWith("e3");
  // One refetch for the whole run.
  expect(invalidate.mock.calls.filter(([filters]) => filters?.queryKey === realtimeKey)).toHaveLength(1);
  expect(screen.getByRole("checkbox", { name: "Select e2" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "Select e1" })).not.toBeChecked();
});

it("disables only the row whose retry is in flight", async () => {
  let release!: () => void;
  vi.spyOn(realtime, "retryRealtimeEvent").mockImplementation(() => new Promise<void>((resolve) => { release = resolve; }));
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RealtimeView /></QueryClientProvider>);
  await screen.findByRole("checkbox", { name: "Select e1" });
  const buttons = () => screen.getAllByRole("button", { name: "Retry" });
  fireEvent.click(buttons()[0]);
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select e1" })).toBeDisabled());
  expect(screen.getByRole("checkbox", { name: "Select e2" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Retry all loaded (3)" })).toBeEnabled();
  release();
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Select e1" })).toBeEnabled());
});

it("keeps at most ten pages of dead events and offers a way back to the newest", async () => {
  vi.spyOn(realtime, "listDeadRealtimeEvents").mockImplementation(async (before) => {
    const page = before === undefined ? 0 : Number(before);
    return { items: [event(`p${page}`)], nextBefore: String(page + 1) };
  });
  Element.prototype.scrollIntoView = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><RealtimeView /></QueryClientProvider>);
  await screen.findByRole("checkbox", { name: "Select p0" });
  for (let page = 1; page <= 10; page++) {
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await screen.findByRole("checkbox", { name: `Select p${page}` });
  }
  expect(screen.queryByRole("checkbox", { name: "Select p0" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Retry all loaded (10)" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Back to newest" }));
  expect(await screen.findByRole("checkbox", { name: "Select p0" })).toBeInTheDocument();
});
