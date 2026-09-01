import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "../app";
import { activeSessionExpiresAt } from "../test/fixtures";

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers({ "content-type": "application/json" }), text: vi.fn().mockResolvedValue(body === undefined ? "" : JSON.stringify(body)) } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/admin/");
});

it("shows relation field counts in the collection catalog", async () => {
  window.history.replaceState(null, "", "/admin/collections");
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(response(200, {
      administrator: { id: "admin-id", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" },
      csrfToken: "csrf-token",
      expiresAt: activeSessionExpiresAt(),
    }))
    .mockResolvedValueOnce(response(200, {
      version: 5,
      collections: {
        posts: {
          id: "posts-id", name: "posts", tableName: "posts",
          fields: [
            { id: "title-id", name: "title", columnName: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 0 },
            {
              id: "owner-id", name: "owner", columnName: "owner", type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
              relation: {
                targetCollectionId: "users-id", cardinality: "single", minSelect: 0, maxSelect: 1,
                onDelete: "restrict", valueCodecVersion: 1, storage: { kind: "inline_fk_v1", name: "owner" },
              },
            },
          ],
        },
      },
    }));
  vi.stubGlobal("fetch", fetchMock);
  render(<App />);

  expect(await screen.findByText("2 fields · 1 relation")).toBeVisible();
});

it("ends the loading state when the collections request fails at the network boundary", async () => {
  window.history.replaceState(null, "", "/admin/collections");
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(response(200, {
      administrator: { id: "admin-id", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" },
      csrfToken: "csrf-token",
      expiresAt: activeSessionExpiresAt(),
    }))
    .mockRejectedValueOnce(new TypeError("Failed to fetch"));
  vi.stubGlobal("fetch", fetchMock);
  render(<App />);

  expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load collections.");
  expect(screen.queryByText("Loading collections…")).not.toBeInTheDocument();
});
