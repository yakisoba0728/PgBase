import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import {
  collectionFromCatalog,
  collectionFromCatalogByID,
  type CollectionCatalog,
  type CollectionDefinition,
} from "../api/collections";
import { activeSessionExpiresAt } from "../test/fixtures";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const ownerID = "abcdefab-cdef-4abc-8def-abcdefabcdef";
const firstTagID = "11111111-1111-4111-8111-111111111111";
const secondTagID = "22222222-2222-4222-8222-222222222222";
const thirdTagID = "33333333-3333-4333-8333-333333333333";
const fourthTagID = "44444444-4444-4444-8444-444444444444";

function response(status: number, body?: unknown, headers?: HeadersInit): Response {
  const responseHeaders = new Headers({ "content-type": "application/json", ...headers });
  if (status >= 200 && status < 300 && !responseHeaders.has("ETag")) responseHeaders.set("ETag", '"pgb1.test"');
  return { ok: status >= 200 && status < 300, status, headers: responseHeaders, text: vi.fn().mockResolvedValue(body === undefined ? "" : JSON.stringify(body)) } as unknown as Response;
}

function session(): Response {
  return response(200, { administrator, csrfToken: "csrf-token", expiresAt: activeSessionExpiresAt() });
}

function relationCatalog(minSelect = 0, maxSelect = 3) {
  return {
    version: 7,
    collections: {
      posts: {
        id: "posts-id",
        name: "posts",
        tableName: "posts",
        fields: [
          { name: "title", type: "text", required: false, unique: false, indexed: true, ordinal: 0 },
          {
            name: "owner", type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
            relation: {
              targetCollectionId: "users-id", cardinality: "single", minSelect: 0, maxSelect: 1,
              onDelete: "set_null", valueCodecVersion: 1, storage: { kind: "inline_fk_v1", name: "owner" },
            },
          },
          {
            name: "tags", type: "relation", required: minSelect > 0, unique: false, indexed: true, ordinal: 2,
            relation: {
              targetCollectionId: "tags-id", cardinality: "multiple", minSelect, maxSelect,
              onDelete: "restrict", valueCodecVersion: 1, storage: { kind: "edge_table_v1", name: "rel_posts_tags" },
            },
          },
        ],
      },
      users: { id: "users-id", name: "users", tableName: "users", fields: [] },
      tags: { id: "tags-id", name: "tags", tableName: "tags", fields: [] },
    },
  };
}

function searchableRelationCatalog(maxSelect = 3) {
  const catalog = relationCatalog(0, maxSelect);
  return {
    ...catalog,
    collections: {
      ...catalog.collections,
      users: {
        ...catalog.collections.users,
        fields: [
          { name: "biography", type: "text", required: false, unique: false, indexed: false, ordinal: 0 },
          { name: "name", type: "text", required: true, unique: false, indexed: true, ordinal: 1 },
        ],
      },
      tags: {
        ...catalog.collections.tags,
        fields: [
          { name: "label", type: "text", required: true, unique: true, indexed: false, ordinal: 0 },
        ],
      },
    },
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/admin/");
});

describe("relation record administration", () => {
  it("only searches targets on explicit action, escapes text literals, and selects a single result", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const catalog = searchableRelationCatalog();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [{ id: ownerID, name: "O'Brien" }] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("owner state"), "value");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await user.type(screen.getByLabelText("owner target name"), "O'Brien");
    await user.click(screen.getByRole("button", { name: "Search owner targets" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/api/v1/collections/users/records?limit=50&filter=name+%3D+%27O%27%27Brien%27&sort=name",
      expect.objectContaining({ credentials: "include" }),
    );
    await user.click(screen.getByRole("button", { name: `Select O'Brien — ${ownerID}` }));
    expect(screen.getByLabelText("owner")).toHaveValue(ownerID);
  });

  it("appends multi selections in result order without duplicates or exceeding maxSelect", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const catalog = searchableRelationCatalog(2);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, {
        items: [
          { id: secondTagID, label: "shared" },
          { id: firstTagID, label: "shared" },
          { id: thirdTagID, label: "shared" },
        ],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("tags state"), "value");
    await user.type(screen.getByLabelText("tags target label"), "shared");
    await user.click(screen.getByRole("button", { name: "Search tags targets" }));

    const second = await screen.findByRole("button", { name: `Select shared — ${secondTagID}` });
    const first = screen.getByRole("button", { name: `Select shared — ${firstTagID}` });
    const third = screen.getByRole("button", { name: `Select shared — ${thirdTagID}` });
    await user.click(second);
    await user.click(first);
    expect(screen.getByLabelText("tags")).toHaveValue(`${secondTagID}\n${firstTagID}`);

    await user.click(second);
    expect(screen.getByText("That target is already selected.")).toBeVisible();
    expect(screen.getByLabelText("tags")).toHaveValue(`${secondTagID}\n${firstTagID}`);
    await user.click(third);
    expect(screen.getByText("Select no more than 2 related records.")).toBeVisible();
    expect(screen.getByLabelText("tags")).toHaveValue(`${secondTagID}\n${firstTagID}`);
  });

  it("falls back to an exact canonical UUID lookup when the target has no searchable text field", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const catalog = relationCatalog();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [{ id: "not-a-uuid" }, { id: ownerID }] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("owner state"), "value");
    await user.type(screen.getByLabelText("owner target UUID"), "not-a-uuid");
    await user.click(screen.getByRole("button", { name: "Search owner targets" }));
    expect(await screen.findByText("Enter a canonical, non-zero target UUID to search.")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await user.clear(screen.getByLabelText("owner target UUID"));
    await user.type(screen.getByLabelText("owner target UUID"), ownerID.toUpperCase());
    await user.click(screen.getByRole("button", { name: "Search owner targets" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(fetchMock).toHaveBeenLastCalledWith(
      `/api/v1/collections/users/records?limit=50&filter=id+%3D+%27${ownerID}%27`,
      expect.objectContaining({ credentials: "include" }),
    );
    expect(screen.queryByRole("button", { name: "Select not-a-uuid" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: `Select ${ownerID}` }));
    expect(screen.getByLabelText("owner")).toHaveValue(ownerID);
  });

  it("creates canonical single and ordered multiple relation values while omitting untouched fields", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const catalog = relationCatalog();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(201, { id: recordID, owner: ownerID, tags: [secondTagID, firstTagID] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("owner state"), "value");
    await user.type(screen.getByLabelText("owner"), ownerID.toUpperCase());
    await user.selectOptions(screen.getByLabelText("tags state"), "value");
    fireEvent.change(screen.getByLabelText("tags"), { target: { value: `${secondTagID.toUpperCase()}\n${firstTagID}` } });
    expect(screen.getByText(/Target: users/)).toBeVisible();
    expect(screen.getByText(/order is preserved/)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Create record" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [path, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(path).toBe("/api/v1/collections/posts/records");
    expect(JSON.parse(options.body as string)).toEqual({ owner: ownerID, tags: [secondTagID, firstTagID] });
    expect(JSON.parse(options.body as string)).not.toHaveProperty("title");
  });

  it("leaves unchanged relation values out of PATCH requests", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const catalog = relationCatalog();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { id: recordID, title: "old", owner: ownerID, tags: [secondTagID, firstTagID] }))
      .mockResolvedValueOnce(response(200, { id: recordID, title: "new", owner: ownerID, tags: [secondTagID, firstTagID] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByLabelText("owner")).toHaveValue(ownerID);
    expect(screen.getByLabelText("tags")).toHaveValue(`${secondTagID}\n${firstTagID}`);
    await user.clear(screen.getByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "new");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));
    const [, options] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({ title: "new" });
  });

  it("clears a single relation with null and a multiple relation with an empty ordered array", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const catalog = relationCatalog();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { id: recordID, title: "old", owner: ownerID, tags: [firstTagID] }))
      .mockResolvedValueOnce(response(200, { id: recordID, title: "old", owner: null, tags: [] }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("owner state"), "null");
    await user.clear(screen.getByLabelText("tags"));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));
    const [, options] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({ owner: null, tags: [] });
  });

  it("rejects invalid UUIDs, duplicates, and values outside relation bounds before making a request", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, relationCatalog(2, 3)));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByLabelText("tags state");
    await user.click(screen.getByRole("button", { name: "Create record" }));
    expect(await screen.findByText("Select at least 2 related records.")).toBeVisible();

    await user.selectOptions(screen.getByLabelText("owner state"), "value");
    await user.type(screen.getByLabelText("owner"), "not-a-uuid");
    await user.selectOptions(screen.getByLabelText("tags state"), "value");
    fireEvent.change(screen.getByLabelText("tags"), { target: { value: `${firstTagID}\n${firstTagID.toUpperCase()}` } });
    await user.click(screen.getByRole("button", { name: "Create record" }));
    expect(await screen.findByText("Enter a canonical, non-zero UUID.")).toBeVisible();
    expect(screen.getByText("Related record IDs must be unique.")).toBeVisible();

    fireEvent.change(screen.getByLabelText("tags"), { target: { value: `${firstTagID}\n${secondTagID}\n${thirdTagID}\n${fourthTagID}` } });
    await user.click(screen.getByRole("button", { name: "Create record" }));
    expect(await screen.findByText("Select no more than 3 related records.")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("maps backend relation failures to safe field-specific messages", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, relationCatalog()))
      .mockResolvedValueOnce(response(422, {
        error: {
          code: "validation_failed",
          message: "unsafe database detail",
          details: { owner: ["relation_target_invalid"], tags: ["duplicate_relation_value"] },
        },
      }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("owner state"), "value");
    await user.type(screen.getByLabelText("owner"), ownerID);
    await user.click(screen.getByRole("button", { name: "Create record" }));

    expect(await screen.findByText("One or more related records do not exist or are unavailable.")).toBeVisible();
    expect(screen.getByText("Related record IDs must be unique.")).toBeVisible();
    expect(screen.queryByText("unsafe database detail")).not.toBeInTheDocument();
  });

  it("renders multiple relations in order and only offers sorting for an indexed single relation", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records");
    const catalog = relationCatalog();
    const item = { id: recordID, title: "post", owner: ownerID, tags: [secondTagID, firstTagID] };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [item] }))
      .mockResolvedValueOnce(response(200, { items: [item] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    const list = await screen.findByRole("list", { name: "tags related record IDs" });
    expect(within(list).getAllByRole("listitem").map((item) => item.textContent)).toEqual([secondTagID, firstTagID]);
    expect(screen.getByRole("button", { name: "owner" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "tags" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "owner" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/api/v1/collections/posts/records?limit=50&sort=owner",
      expect.objectContaining({ credentials: "include" }),
    );
  });

  it("looks up source and target collections without traversing inherited catalog properties", () => {
    const inherited: CollectionDefinition = { id: "inherited-id", name: "inherited", tableName: "inherited", fields: [] };
    const own: CollectionDefinition = { id: "own-id", name: "constructor", tableName: "constructor", fields: [] };
    const collections = Object.create({ inherited, shadow: inherited }) as Record<string, CollectionDefinition>;
    Object.defineProperty(collections, "constructor", { configurable: true, enumerable: true, value: own });
    const catalog = { version: 1, collections } satisfies CollectionCatalog;

    expect(collectionFromCatalog(catalog, "shadow")).toBeUndefined();
    expect(collectionFromCatalog(catalog, "constructor")).toBe(own);
    expect(collectionFromCatalogByID(catalog, "inherited-id")).toBeUndefined();
    expect(collectionFromCatalogByID(catalog, "own-id")).toBe(own);
  });
});
