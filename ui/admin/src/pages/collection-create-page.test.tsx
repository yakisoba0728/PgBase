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
    text: vi.fn().mockResolvedValue(body === undefined ? "" : JSON.stringify(body)),
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
  it("does not silently select self when the collection name is entered after choosing relation type", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, { version: 1, collections: {} }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");
    await user.type(screen.getByLabelText("Collection name"), "nodes");

    expect(await screen.findByRole("option", { name: "nodes (self)" })).toBeVisible();
    expect(screen.getByLabelText("Field 1 relation target")).toHaveValue("");
  });

  it("keeps a chosen relation target when the typed collection name passes through it", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 4,
        collections: {
          users: { id: "users-id", name: "users", tableName: "users", fields: [] },
        },
      }))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 4,
        checksum: "target-checksum",
        definition: {
          name: "users_profiles",
          tableName: "users_profiles",
          fields: [{
            name: "owner", type: "relation", required: false, unique: false, indexed: true, ordinal: 0,
            relation: {
              targetCollection: "users", targetCollectionId: "users-id", cardinality: "single",
              minSelect: 0, maxSelect: 1, onDelete: "restrict", valueCodecVersion: 1,
              storage: { kind: "inline_fk_v1", name: "owner" },
            },
          }],
        },
        sql: ["ALTER TABLE app.users_profiles ADD COLUMN owner uuid"],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Field 1 name"), "owner");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");
    const target = await screen.findByLabelText("Field 1 relation target");
    await waitFor(() => expect(target).toBeEnabled());
    await user.selectOptions(target, "users");

    // Typing the name one keystroke at a time makes it equal the chosen target
    // on the way to its final value.
    await user.type(screen.getByLabelText("Collection name"), "users_profiles");
    expect(target).toHaveValue("users");

    await user.click(screen.getByRole("button", { name: "Preview collection" }));
    expect(await screen.findByText("owner · relation · indexed · target: users · single · min: 0 · max: 1 · on delete: restrict")).toBeVisible();
    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(planOptions.body as string)).toEqual({
      operation: "create",
      name: "users_profiles",
      fields: [{
        name: "owner", type: "relation", required: false, unique: false, indexed: true,
        relation: { targetCollection: "users", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "restrict" },
      }],
    });
  });

  it("retargets a relation chosen as self when the collection name changes", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 4,
        collections: {
          users: { id: "users-id", name: "users", tableName: "users", fields: [] },
        },
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "node");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");
    const target = await screen.findByLabelText("Field 1 relation target");
    await waitFor(() => expect(target).toBeEnabled());
    await user.selectOptions(target, "node");

    await user.type(screen.getByLabelText("Collection name"), "s");
    expect(target).toHaveValue("nodes");
    expect(await screen.findByRole("option", { name: "nodes (self)" })).toBeVisible();
  });

  it("builds a self relation from the live catalog and previews normalized relation SQL", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 4,
        collections: {
          users: { id: "users-id", name: "users", tableName: "users", fields: [] },
        },
      }))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 4,
        checksum: "relation-checksum",
        definition: {
          name: "nodes",
          tableName: "nodes",
          fields: [{
            name: "parent", type: "relation", required: false, unique: false, indexed: true, ordinal: 0,
            relation: {
              targetCollection: "nodes", cardinality: "single", minSelect: 0, maxSelect: 1,
              onDelete: "set_null", valueCodecVersion: 1,
              storage: { kind: "inline_fk_v1", name: "parent" },
            },
          }],
        },
        sql: ["ALTER TABLE app.nodes ADD CONSTRAINT relation_parent ..."],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "nodes");
    await user.type(screen.getByLabelText("Field 1 name"), "parent");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");

    const target = await screen.findByLabelText("Field 1 relation target");
    await waitFor(() => expect(target).toBeEnabled());
    expect(screen.getByRole("option", { name: "nodes (self)" })).toBeVisible();
    expect(screen.getByRole("option", { name: "users" })).toBeVisible();
    await user.selectOptions(target, "nodes");
    await user.selectOptions(screen.getByLabelText("Field 1 relation delete action"), "set_null");
    expect(screen.getByLabelText("Field 1 required")).toBeDisabled();
    expect(screen.getByLabelText("Field 1 indexed")).toBeChecked();
    expect(screen.getByLabelText("Field 1 indexed")).toBeDisabled();
    expect(screen.getByLabelText("Field 1 has default")).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Preview collection" }));
    expect(await screen.findByText("parent · relation · indexed · target: nodes · single · min: 0 · max: 1 · on delete: set_null")).toBeVisible();
    expect(screen.getByText(/ALTER TABLE app\.nodes/)).toBeVisible();

    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(planOptions.body as string)).toEqual({
      operation: "create",
      name: "nodes",
      fields: [{
        name: "parent", type: "relation", required: false, unique: false, indexed: true,
        relation: { targetCollection: "nodes", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "set_null" },
      }],
    });
  });

  it("keeps multiple relation bounds, required state, and uniqueness consistent", async () => {
    window.history.replaceState(null, "", "/admin/collections/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, {
        version: 9,
        collections: {
          tags: { id: "tags-id", name: "tags", tableName: "tags", fields: [] },
        },
      }))
      .mockResolvedValueOnce(response(200, {
        baseVersion: 9,
        checksum: "multiple-checksum",
        definition: {
          name: "posts", tableName: "posts",
          fields: [{
            name: "tags", type: "relation", required: true, unique: false, indexed: true, ordinal: 0,
            relation: {
              targetCollection: "tags", targetCollectionId: "tags-id", cardinality: "multiple",
              minSelect: 2, maxSelect: 20, onDelete: "cascade", valueCodecVersion: 1,
              storage: { kind: "edge_table_v1", name: "rel_table" },
            },
          }],
        },
        sql: ["CREATE TABLE pgbase_relation.rel_table (...)"],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "posts");
    await user.type(screen.getByLabelText("Field 1 name"), "tags");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");
    await waitFor(() => expect(screen.getByLabelText("Field 1 relation target")).toBeEnabled());
    await user.selectOptions(screen.getByLabelText("Field 1 relation target"), "tags");
    await user.click(screen.getByLabelText("Field 1 unique"));
    await user.selectOptions(screen.getByLabelText("Field 1 relation cardinality"), "multiple");

    const minimum = screen.getByLabelText("Field 1 relation minimum");
    const maximum = screen.getByLabelText("Field 1 relation maximum");
    expect(maximum).toHaveAttribute("min", "2");
    expect(maximum).toHaveAttribute("max", "1000");
    expect(maximum).toHaveValue(20);
    expect(screen.getByLabelText("Field 1 unique")).not.toBeChecked();
    expect(screen.getByLabelText("Field 1 unique")).toBeDisabled();
    await user.clear(minimum);
    await user.type(minimum, "2");
    expect(screen.getByLabelText("Field 1 required")).toBeChecked();
    await user.selectOptions(screen.getByLabelText("Field 1 relation delete action"), "cascade");
    await user.click(screen.getByRole("button", { name: "Preview collection" }));

    await screen.findByText(/CREATE TABLE pgbase_relation\.rel_table/);
    const [, planOptions] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(planOptions.body as string)).toMatchObject({
      fields: [{
        required: true,
        unique: false,
        indexed: true,
        relation: { targetCollection: "tags", cardinality: "multiple", minSelect: 2, maxSelect: 20, onDelete: "cascade" },
      }],
    });
  });

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
    expect(new Headers(planOptions.headers).get("X-PgBase-CSRF")).toBe("csrf-token");
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
      .mockResolvedValueOnce(response(409, { error: { code: "schema_conflict", message: "unsafe detail", details: {} } }))
      .mockResolvedValueOnce(response(200, { version: 3, collections: {} }));
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

    expect(await screen.findByRole("alert")).toHaveTextContent("A collection with this name likely already exists.");
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply collection" })).not.toBeInTheDocument();
    const [, conflictOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(JSON.parse(conflictOptions.body as string)).toMatchObject({ checksum: "checksum-one", baseVersion: 3 });
  });
});
