import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/sql";
import { ApiError } from "../api/http";
import { SqlConsole } from "./SqlConsole";
import { setLanguage } from "../i18n";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("keeps the live SQL editor mounted when another history page fails", async () => {
  setLanguage("en");
  const list = vi.spyOn(api, "listSqlExecutions").mockResolvedValueOnce({ executions: [], nextCursor: "next", sessionIdentity: "s", writeEnabled: false, writeTargets: [], defaultTimeoutMs: 1000, maximumTimeoutMs: 10000 }).mockRejectedValueOnce(new Error("offline"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <SqlConsole /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  const editor = await screen.findByRole("textbox", { name: "SQL statement" });
  fireEvent.change(editor, { target: { value: "select 'keep my text'" } });
  fireEvent.click(screen.getByRole("button", { name: "Load more" }));
  await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  await screen.findByRole("button", { name: "Retry" });
  expect(screen.getByRole("textbox", { name: "SQL statement" })).toBe(editor);
  expect(editor).toHaveValue("select 'keep my text'");
  cleanup(); client.clear();
});

it("keeps a running result and its Cancel button, with a retry, when one status poll fails", async () => {
  setLanguage("en");
  const { SqlResult } = await import("./SqlResult");
  const { sqlExecutionQuery } = await import("./queries");
  vi.spyOn(api, "getSqlExecution").mockRejectedValue(new Error("offline"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(sqlExecutionQuery("e1").queryKey, { id: "e1", mode: "read", dryRun: false, status: "running", createdAt: "2026-01-01T00:00:00Z" });
  render(<QueryClientProvider client={client}><SqlResult id="e1" sql="select 1" /></QueryClientProvider>);
  await waitFor(() => expect(client.getQueryState(sqlExecutionQuery("e1").queryKey)?.status).toBe("error"));
  expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
  cleanup(); client.clear();
});

it("explains a server without the SQL console instead of an error", async () => {
  setLanguage("en");
  vi.spyOn(api, "listSqlExecutions").mockRejectedValue(new ApiError(404, "not_found", "Not found"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <SqlConsole /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  expect(await screen.findByRole("heading", { name: "The SQL console is off" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
  cleanup(); client.clear();
});
