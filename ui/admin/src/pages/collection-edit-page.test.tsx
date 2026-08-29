import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { activeSessionExpiresAt } from "../test/fixtures";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers({ "content-type": "application/json" }), json: vi.fn().mockResolvedValue(body) } as unknown as Response;
}

function session(): Response {
  return response(200, { administrator, csrfToken: "csrf-token", expiresAt: activeSessionExpiresAt() });
}

const catalog = {
  version: 7,
  collections: {
    posts: {
      id: "collection-id", name: "posts", tableName: "posts",
      fields: [{ id: "field-id", name: "title", columnName: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 }],
    },
  },
};

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/admin/");
});

describe("collection editing", () => {
  it("previews and applies a schema update with the update operation", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7,
        checksum: "update-checksum",
        definition: { name: "posts", tableName: "posts", fields: [
          { name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 },
          { name: "views", type: "bigint", required: false, unique: false, indexed: false, ordinal: 1 },
        ] },
        sql: ["ALTER TABLE app.posts ADD COLUMN views bigint"],
      }))
      .mockResolvedValueOnce(response(200, { id: "collection-id", name: "posts", fields: [] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    expect(await screen.findByLabelText("Field 1 name")).toHaveValue("title");
    expect(screen.getByLabelText("Field 1 required")).toBeChecked();

    await user.click(screen.getByRole("button", { name: "Add field" }));
    await user.type(screen.getByLabelText("Field 2 name"), "views");
    await user.selectOptions(screen.getByLabelText("Field 2 type"), "bigint");
    await user.click(screen.getByRole("button", { name: "Preview change" }));

    expect(await screen.findByText(/ALTER TABLE app\.posts/)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Apply change" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));

    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    const [applyPath, applyOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(applyPath).toBe("/api/admin/collections/apply");
    const planBody = JSON.parse(planOptions.body as string) as Record<string, unknown>;
    expect(planBody).toMatchObject({ operation: "update", name: "posts" });
    expect(JSON.parse(applyOptions.body as string)).toEqual({
      ...planBody,
      baseVersion: 7,
      checksum: "update-checksum",
    });
    expect(new Headers(applyOptions.headers).get("X-PgBase-CSRF")).toBe("csrf-token");
    expect(window.location.pathname).toBe("/admin/collections/posts/records");
  });

  it("requires confirming the deletion plan before dropping the collection", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 8,
        checksum: "delete-checksum",
        definition: { name: "posts", tableName: "posts", fields: [] },
        sql: ["DROP TABLE app.posts"],
      }))
      .mockResolvedValueOnce(response(200, { name: "posts", deleted: true }))
      .mockResolvedValueOnce(response(200, { version: 9, collections: {} }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    await user.click(await screen.findByRole("button", { name: "Delete collection" }));
    expect(await screen.findByText(/DROP TABLE app\.posts/)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));

    const [applyPath, applyOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(applyPath).toBe("/api/admin/collections/apply");
    expect(JSON.parse(applyOptions.body as string)).toEqual({
      operation: "delete", name: "posts", checksum: "delete-checksum", baseVersion: 8,
    });
    expect(window.location.pathname).toBe("/admin/collections");
  });

  it("reports missing collections safely", async () => {
    window.history.replaceState(null, "", "/admin/collections/ghosts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, { version: 1, collections: {} }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByText("Collection not found.")).toBeVisible();
  });
});
