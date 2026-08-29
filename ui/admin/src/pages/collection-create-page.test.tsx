import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { activeSessionExpiresAt } from "../test/fixtures";

const administrator = {
  id: "9a2e0000-0000-4000-8000-000000000001",
  email: "admin@example.com",
  createdAt: "2026-08-28T00:00:00Z",
};

function response(status: number, body?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json" }),
    json: vi.fn().mockResolvedValue(body),
  } as unknown as Response;
}

function session(): Response {
  return response(200, { administrator, csrfToken: "csrf-token", expiresAt: activeSessionExpiresAt() });
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/admin/");
});

describe("collection creation", () => {
  it("previews normalized server output and applies the exact preview input and checksum", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7,
        checksum: "server-checksum",
        definition: {
          name: "posts",
          tableName: "posts",
          fields: [
            { name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 },
            { name: "views", type: "bigint", required: false, unique: false, indexed: false, default: "0", ordinal: 1 },
          ],
        },
        sql: ["CREATE TABLE app.posts (...)", "CREATE INDEX posts_title_idx (...)"],
      }))
      .mockResolvedValueOnce(response(201, { id: "collection-id", name: "posts", fields: [] }))
      .mockResolvedValueOnce(response(200, { version: 8, collections: {} }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "posts");
    await user.type(screen.getByLabelText("Field 1 name"), "title");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "text");
    await user.click(screen.getByLabelText("Field 1 required"));
    await user.click(screen.getByLabelText("Field 1 indexed"));
    await user.click(screen.getByRole("button", { name: "Add field" }));
    await user.type(screen.getByLabelText("Field 2 name"), "views");
    await user.selectOptions(screen.getByLabelText("Field 2 type"), "bigint");
    await user.click(screen.getByLabelText("Field 2 has default"));
    await user.type(screen.getByLabelText("Field 2 default"), "0");
    await user.click(screen.getByRole("button", { name: "Preview collection" }));

    expect(await screen.findByText("Catalog version 7")).toBeVisible();
    expect(screen.getByText("title · text · required · indexed")).toBeVisible();
    expect(screen.getByText("views · bigint · default: 0")).toBeVisible();
    expect(screen.getByText(/CREATE TABLE app\.posts/)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Apply collection" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));

    const [, planOptions] = fetchMock.mock.calls[1] as [string, RequestInit];
    const [applyPath, applyOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(applyPath).toBe("/api/admin/collections/apply");
    expect(JSON.parse(planOptions.body as string)).toMatchObject({ operation: "create", name: "posts" });
    const plannedInput = JSON.parse(planOptions.body as string) as unknown;
    expect(JSON.parse(applyOptions.body as string)).toEqual({
      ...(plannedInput as object),
      baseVersion: 7,
      checksum: "server-checksum",
    });
    expect(new Headers(applyOptions.headers).get("X-PgBase-CSRF")).toBe("csrf-token");
    expect(window.location.pathname).toBe("/admin/collections");
  });

  it("invalidates changed previews and requires a fresh preview after a conflict", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const planBody = {
      baseVersion: 3,
      checksum: "checksum-one",
      definition: { name: "posts", tableName: "posts", fields: [{ name: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 0 }] },
      sql: ["CREATE TABLE app.posts (...)"],
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, planBody))
      .mockResolvedValueOnce(response(200, planBody))
      .mockResolvedValueOnce(response(409, { error: { code: "schema_conflict", message: "unsafe detail", details: {} } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "posts");
    await user.type(screen.getByLabelText("Field 1 name"), "title");
    await user.click(screen.getByRole("button", { name: "Preview collection" }));
    expect(await screen.findByRole("button", { name: "Apply collection" })).toBeEnabled();

    await user.type(screen.getByLabelText("Field 1 name"), " changed");
    expect(screen.queryByRole("button", { name: "Apply collection" })).not.toBeInTheDocument();
    await user.clear(screen.getByLabelText("Field 1 name"));
    await user.type(screen.getByLabelText("Field 1 name"), "title");
    await user.click(screen.getByRole("button", { name: "Preview collection" }));
    await screen.findByRole("button", { name: "Apply collection" });
    await user.click(screen.getByRole("button", { name: "Apply collection" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("The catalog changed. Preview the collection again before applying.");
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply collection" })).not.toBeInTheDocument();
    const [, conflictOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(JSON.parse(conflictOptions.body as string)).toMatchObject({ checksum: "checksum-one", baseVersion: 3 });
  });
});
