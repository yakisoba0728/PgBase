import { cleanup, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import * as schema from "../api/schema";
import * as storage from "../api/storage";
import { setLanguage } from "../i18n";
import { RunningFeed } from "./RunningFeed";

const flag = "pgbase.operations.discovered";

function renderFeed() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <RunningFeed /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}

beforeEach(() => {
  setLanguage("en");
  sessionStorage.clear();
  // No migration ever ran: the latest-migration route answers 404.
  vi.spyOn(storage, "getLatestStorageMigration").mockRejectedValue(new ApiError(404, "not_found", "none"));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); sessionStorage.clear(); });

it("settles discovery for the tab when a lookup is refused, so the 404 is not asked again on every load", async () => {
  vi.spyOn(schema, "listSchemaOperations").mockRejectedValue(new ApiError(403, "forbidden", "no"));
  renderFeed();
  await waitFor(() => expect(sessionStorage.getItem(flag)).toBe("1"));
  expect(storage.getLatestStorageMigration).toHaveBeenCalledTimes(1);
});

it("keeps discovery for the next load when the session ended", async () => {
  vi.spyOn(schema, "listSchemaOperations").mockRejectedValue(new ApiError(401, "unauthorized", "gone"));
  renderFeed();
  await waitFor(() => expect(schema.listSchemaOperations).toHaveBeenCalled());
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(sessionStorage.getItem(flag)).toBeNull();
});
