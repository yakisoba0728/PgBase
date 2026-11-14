import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/schema";
import { setLanguage } from "../i18n";
import { SchemaOperations } from "./SchemaOperations";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("keeps the ledger and an open receipt on screen, with a retry, when a background poll fails", async () => {
  setLanguage("en");
  const receipt: api.OperationReceipt = { operationId: "op-1", operation: "collection_create", collectionId: "c1", status: "running", baseVersion: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" };
  const list = vi.spyOn(api, "listSchemaOperations").mockResolvedValueOnce({ items: [receipt] } as api.OperationPage).mockRejectedValue(new Error("offline"));
  const one = vi.spyOn(api, "getSchemaOperation").mockResolvedValueOnce(receipt).mockRejectedValue(new Error("offline"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <SchemaOperations /> }], { initialEntries: ["/?op=op-1"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  await screen.findByRole("heading", { name: "Raw JSON" });
  await waitFor(() => { expect(list).toHaveBeenCalledTimes(2); expect(one).toHaveBeenCalledTimes(2); }, { timeout: 3000 });
  await waitFor(() => expect(client.getQueryState(["schema-operations", "one", "op-1"])?.status).toBe("error"));
  expect(screen.getByRole("table")).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Raw JSON" })).toBeInTheDocument();
  expect(await screen.findAllByRole("button", { name: "Retry" })).toHaveLength(2);
  cleanup(); client.clear();
});
