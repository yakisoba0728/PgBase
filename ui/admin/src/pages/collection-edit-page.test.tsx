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
  it("resolves persisted relation target IDs back to names and round-trips relation settings", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const relationCatalog = {
      version: 12,
      collections: {
        posts: {
          id: "posts-id", name: "posts", tableName: "posts",
          fields: [{
            id: "authors-field-id", name: "authors", columnName: "authors", type: "relation",
            required: true, unique: false, indexed: true, ordinal: 0,
            relation: {
              targetCollectionId: "users-id", cardinality: "multiple", minSelect: 2, maxSelect: 20,
              onDelete: "cascade", valueCodecVersion: 1,
              storage: { kind: "edge_table_v1", name: "rel_authors" },
            },
          }],
        },
        users: { id: "users-id", name: "users", tableName: "users", fields: [] },
      },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, relationCatalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 12,
        checksum: "relation-update-checksum",
        definition: {
          name: "posts", tableName: "posts",
          fields: [{
            name: "authors", type: "relation", required: true, unique: false, indexed: true, ordinal: 0,
            relation: {
              targetCollection: "users", targetCollectionId: "users-id", cardinality: "multiple",
              minSelect: 2, maxSelect: 20, onDelete: "cascade", valueCodecVersion: 1,
              storage: { kind: "edge_table_v1", name: "rel_authors" },
            },
          }],
        },
        sql: [],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    expect(await screen.findByLabelText("Field 1 relation target")).toHaveValue("users");
    expect(screen.getByLabelText("Field 1 relation cardinality")).toHaveValue("multiple");
    expect(screen.getByLabelText("Field 1 relation minimum")).toHaveValue(2);
    expect(screen.getByLabelText("Field 1 relation maximum")).toHaveValue(20);
    expect(screen.getByLabelText("Field 1 relation delete action")).toHaveValue("cascade");
    expect(screen.getByLabelText("Field 1 required")).toBeChecked();
    expect(screen.getByLabelText("Field 1 unique")).toBeDisabled();
    expect(screen.queryByText(/New required relations use a staged migration/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Preview change" }));
    expect(await screen.findByText("authors · relation · required · indexed · target: users · multiple · min: 2 · max: 20 · on delete: cascade")).toBeVisible();
    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(planOptions.body as string)).toEqual({
      operation: "update",
      name: "posts",
      fields: [{
        name: "authors", type: "relation", required: true, unique: false, indexed: true,
        relation: { targetCollection: "users", cardinality: "multiple", minSelect: 2, maxSelect: 20, onDelete: "cascade" },
      }],
    });
  });

  it("explains staged required relation migration and displays the backend validation code", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const catalogWithTarget = {
      ...catalog,
      collections: {
        ...catalog.collections,
        users: { id: "users-id", name: "users", tableName: "users", fields: [] },
      },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalogWithTarget))
      .mockResolvedValueOnce(response(400, {
        error: {
          code: "validation_failed",
          message: "unsafe internal detail",
          details: { "fields.1.relation": ["relation_required_backfill_needed"] },
        },
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Add field" }));
    await user.type(screen.getByLabelText("Field 2 name"), "owner");
    await user.selectOptions(screen.getByLabelText("Field 2 type"), "relation");
    await user.selectOptions(screen.getByLabelText("Field 2 relation target"), "users");
    await user.click(screen.getByLabelText("Field 2 required"));

    expect(screen.getByRole("note")).toHaveTextContent("add the relation as optional, populate every record, then mark it required");
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("fields.1.relation: relation_required_backfill_needed");
    expect(alert).not.toHaveTextContent("unsafe internal detail");
  });

  it("requires explicit confirmation before applying a change that deletes field data", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const removableCatalog = {
      version: 7,
      collections: {
        posts: {
          id: "collection-id", name: "posts", tableName: "posts",
          fields: [{ id: "field-id", name: "title", columnName: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 0 }],
        },
      },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, removableCatalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7,
        checksum: "rename-checksum",
        definition: { name: "posts", tableName: "posts", fields: [
          { name: "headline", type: "text", required: false, unique: false, indexed: false, ordinal: 0 },
        ] },
        sql: ["ALTER TABLE app.posts DROP COLUMN title", "ALTER TABLE app.posts ADD COLUMN headline text"],
      }))
      .mockResolvedValueOnce(response(200, { id: "collection-id", name: "posts", fields: [] }))
      .mockResolvedValueOnce(response(200, removableCatalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    const name = await screen.findByLabelText("Field 1 name");
    await user.clear(name);
    await user.type(name, "headline");
    await user.click(screen.getByRole("button", { name: "Preview change" }));

    const warning = await screen.findByRole("alert", { name: "Permanent data loss warning" });
    expect(warning).toHaveTextContent("This change permanently deletes data from these fields:");
    expect(warning).toHaveTextContent("title");
    const apply = screen.getByRole("button", { name: "Apply change" });
    expect(apply).toBeDisabled();
    await user.click(apply);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await user.click(screen.getByRole("checkbox", { name: "I understand that these fields and their data will be permanently deleted." }));
    expect(apply).toBeEnabled();
    await user.click(apply);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));
  });

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
    expect(new Headers(planOptions.headers).get("X-PgBase-CSRF")).toBe("csrf-token");
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

  it("round-trips now timestamp defaults without treating them as strings", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 7,
        collections: {
          posts: {
            id: "collection-id", name: "posts", tableName: "posts",
            fields: [{ id: "field-id", name: "published_at", columnName: "published_at", type: "timestamptz", required: false, unique: false, indexed: false, default: { kind: "now" }, ordinal: 0 }],
          },
        },
      }))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7,
        checksum: "now-checksum",
        definition: { name: "posts", tableName: "posts", fields: [
          { name: "published_at", type: "timestamptz", required: false, unique: false, indexed: false, default: { kind: "now" }, ordinal: 0 },
        ] },
        sql: ["ALTER TABLE app.posts ALTER COLUMN published_at SET DEFAULT CURRENT_TIMESTAMP"],
      }))
      .mockResolvedValueOnce(response(200, { id: "collection-id", name: "posts", fields: [] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    expect(await screen.findByLabelText("Field 1 default")).toHaveValue("now");
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    expect(await screen.findByText(/ALTER TABLE app\.posts/)).toBeVisible();

    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    const planBody = JSON.parse(planOptions.body as string) as { fields: Array<{ default: unknown }> };
    expect(planBody.fields[0].default).toEqual({ kind: "now" });
  });

  it("round-trips jsonb defaults, including string scalars, without changing their type", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 7,
        collections: {
          posts: {
            id: "collection-id", name: "posts", tableName: "posts",
            fields: [
              { id: "f1", name: "payload", columnName: "payload", type: "jsonb", required: false, unique: false, indexed: false, default: { tags: ["safe"] }, ordinal: 0 },
              { id: "f2", name: "label", columnName: "label", type: "jsonb", required: false, unique: false, indexed: false, default: "[1,2]", ordinal: 1 },
            ],
          },
        },
      }))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7,
        checksum: "jsonb-checksum",
        definition: { name: "posts", tableName: "posts", fields: [
          { name: "payload", type: "jsonb", required: false, unique: false, indexed: false, default: { tags: ["safe"] }, ordinal: 0 },
          { name: "label", type: "jsonb", required: false, unique: false, indexed: false, default: "[1,2]", ordinal: 1 },
        ] },
        sql: [],
      }))
      .mockResolvedValueOnce(response(200, { id: "collection-id", name: "posts", fields: [] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    expect(await screen.findByLabelText("Field 2 default")).toHaveValue('"[1,2]"');
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    expect(await screen.findByRole("button", { name: "Apply change" })).toBeEnabled();

    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    const planBody = JSON.parse(planOptions.body as string) as { fields: Array<{ default: unknown }> };
    expect(planBody.fields[0].default).toEqual({ tags: ["safe"] });
    expect(planBody.fields[1].default).toBe("[1,2]");
  });

  it("distinguishes a data-conflict 409 from a catalog-change 409 when applying", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7, checksum: "conflict-checksum",
        definition: { name: "posts", tableName: "posts", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 }] },
        sql: ["CREATE UNIQUE INDEX ..."],
      }))
      .mockResolvedValueOnce(response(409, { error: { code: "schema_conflict", message: "unsafe detail", details: {} } }))
      .mockResolvedValueOnce(response(200, { version: 7, collections: catalog.collections }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    const firstView = render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    await user.click(await screen.findByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));
    expect(await screen.findByText("The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it.")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(5);
    firstView.unmount();

    // A stale plan (catalog actually moved) keeps the re-preview guidance.
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const staleMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7, checksum: "stale-checksum",
        definition: { name: "posts", tableName: "posts", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 }] },
        sql: ["CREATE INDEX ..."],
      }))
      .mockResolvedValueOnce(response(409, { error: { code: "schema_conflict", message: "unsafe detail", details: {} } }))
      .mockResolvedValueOnce(response(200, { version: 9, collections: catalog.collections }));
    vi.stubGlobal("fetch", staleMock);
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    await user.click(await screen.findByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));
    expect(await screen.findByText("The catalog changed. Preview the collection again before applying.")).toBeVisible();
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
  });

  it("explains a 422 data-constraint rejection with the existing-data message", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 7, checksum: "data-checksum",
        definition: { name: "posts", tableName: "posts", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0 }] },
        sql: ["ALTER TABLE app.posts ALTER COLUMN title SET NOT NULL"],
      }))
      .mockResolvedValueOnce(response(422, { error: { code: "validation_failed", message: "unsafe detail", details: {} } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit collection" });
    await user.click(await screen.findByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));
    expect(await screen.findByText("Existing data blocks this change (for example missing or duplicate values).")).toBeVisible();
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
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

  it("shows the stable collection-referenced code when a relation blocks deletion", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(409, {
        error: {
          code: "collection_referenced",
          message: "unsafe source relation",
          details: { name: ["collection_referenced"] },
        },
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Delete collection" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Collection is referenced. name: collection_referenced");
    expect(alert).not.toHaveTextContent("unsafe source relation");
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

  it("treats an inherited collection name as missing on the collection editor", async () => {
    window.history.replaceState(null, "", "/admin/collections/constructor/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, { version: 1, collections: {} }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Collection not found.");
  });

  it.each([
    ["server", () => Promise.resolve(response(503, { error: { code: "unavailable", message: "unsafe detail", details: {} } }))],
    ["network", () => Promise.reject(new TypeError("Failed to fetch"))],
  ])("distinguishes a %s catalog failure from a missing collection", async (_kind, catalogFailure) => {
    window.history.replaceState(null, "", "/admin/collections/posts/edit");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockImplementationOnce(catalogFailure);
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load collection.");
    expect(screen.queryByText("Collection not found.")).not.toBeInTheDocument();
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
  });
});
