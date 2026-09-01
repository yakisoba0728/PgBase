import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "./app";
import { activeSessionExpiresAt } from "./test/fixtures";

const administrator = {
  id: "9a2e0000-0000-4000-8000-000000000001",
  email: "admin@example.com",
  createdAt: "2026-08-28T00:00:00Z",
};

const catalog = {
  version: 4,
  collections: {
    posts: {
      id: "posts-id",
      name: "posts",
      tableName: "posts",
      fields: [
        { id: "post-title-id", name: "title", columnName: "title", type: "text", required: false, unique: false, indexed: true, ordinal: 0 },
      ],
    },
    users: {
      id: "users-id",
      name: "users",
      tableName: "users",
      fields: [
        { id: "user-name-id", name: "name", columnName: "name", type: "text", required: false, unique: false, indexed: true, ordinal: 0 },
      ],
    },
  },
};

function response(status: number, body?: unknown, headers?: HeadersInit): Response {
  const responseHeaders = new Headers({ "content-type": "application/json", ...headers });
  if (status >= 200 && status < 300 && !responseHeaders.has("ETag")) responseHeaders.set("ETag", '"pgb1.test"');
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: responseHeaders,
    text: vi.fn().mockResolvedValue(body === undefined ? "" : JSON.stringify(body)),
  } as unknown as Response;
}

function session(): Response {
  return response(200, { administrator, csrfToken: "csrf-token", expiresAt: activeSessionExpiresAt() });
}

function visit(path: string): void {
  window.history.pushState(null, "", path);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

async function traverseHistory(direction: "back" | "forward"): Promise<void> {
  await new Promise<void>((resolve) => {
    window.addEventListener("popstate", () => resolve(), { once: true });
    window.history[direction]();
  });
}

function deferred<T>(): { promise: Promise<T>; resolve(value: T): void } {
  let resolvePromise: ((value: T) => void) | undefined;
  const promise = new Promise<T>((resolve) => { resolvePromise = resolve; });
  return { promise, resolve: (value) => resolvePromise?.(value) };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/admin/");
});

it("resets records state when popstate changes the collection resource", async () => {
  window.history.replaceState(null, "", "/admin/collections/posts/records");
  const fetchMock = vi.fn((path: string) => {
    if (path === "/api/admin/session") return Promise.resolve(session());
    if (path === "/api/admin/collections") return Promise.resolve(response(200, catalog));
    if (path.startsWith("/api/v1/collections/posts/records?")) {
      return Promise.resolve(response(200, { items: [{ id: "post-id", title: "Post A", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }));
    }
    if (path.startsWith("/api/v1/collections/users/records?")) {
      return Promise.resolve(response(200, { items: [{ id: "user-id", name: "User B", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }));
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  expect(await screen.findByText("Post A")).toBeVisible();
  await user.type(screen.getByLabelText("Filter"), "title = 'private draft'");
  await user.click(screen.getByRole("button", { name: "Apply filter" }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
    "/api/v1/collections/posts/records?limit=50&filter=title+%3D+%27private+draft%27",
    expect.objectContaining({ credentials: "include" }),
  ));

  await act(async () => visit("/admin/collections/users/records"));

  expect(await screen.findByRole("heading", { name: "users records" })).toBeVisible();
  expect(await screen.findByText("User B")).toBeVisible();
  expect(screen.getByLabelText("Filter")).toHaveValue("");
  expect(screen.queryByText("Post A")).not.toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/v1/collections/users/records?limit=50",
    expect.objectContaining({ credentials: "include" }),
  );

  await act(async () => traverseHistory("back"));
  expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
  expect(await screen.findByText("Post A")).toBeVisible();
  expect(screen.getByLabelText("Filter")).toHaveValue("");

  await act(async () => traverseHistory("forward"));
  expect(await screen.findByRole("heading", { name: "users records" })).toBeVisible();
  expect(await screen.findByText("User B")).toBeVisible();
  expect(screen.getByLabelText("Filter")).toHaveValue("");
});

it("ignores a completed record mutation after popstate selects another record", async () => {
  const firstID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
  const secondID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff";
  window.history.replaceState(null, "", `/admin/collections/posts/records/${firstID}`);
  const update = deferred<Response>();
  const fetchMock = vi.fn((path: string, options?: RequestInit) => {
    if (path === "/api/admin/session") return Promise.resolve(session());
    if (path === "/api/admin/collections") return Promise.resolve(response(200, catalog));
    if (path === `/api/v1/collections/posts/records/${firstID}` && options?.method === "PATCH") return update.promise;
    if (path === `/api/v1/collections/posts/records/${firstID}`) {
      return Promise.resolve(response(200, { id: firstID, title: "First record" }, { ETag: '"pgb1.first"' }));
    }
    if (path === `/api/v1/collections/posts/records/${secondID}`) {
      return Promise.resolve(response(200, { id: secondID, title: "Second record" }, { ETag: '"pgb1.second"' }));
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  const title = await screen.findByLabelText("title");
  expect(title).toHaveValue("First record");
  await user.clear(title);
  await user.type(title, "First record draft");
  await user.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
    `/api/v1/collections/posts/records/${firstID}`,
    expect.objectContaining({ method: "PATCH" }),
  ));

  await act(async () => visit(`/admin/collections/posts/records/${secondID}`));
  expect(await screen.findByLabelText("title")).toHaveValue("Second record");

  await act(async () => update.resolve(response(200, { id: firstID, title: "First record draft" }, { ETag: '"pgb1.updated"' })));

  expect(window.location.pathname).toBe(`/admin/collections/posts/records/${secondID}`);
  expect(screen.getByRole("heading", { name: "Edit posts record" })).toBeVisible();
  expect(screen.getByLabelText("title")).toHaveValue("Second record");
});

it("ignores a completed collection mutation after popstate selects another collection", async () => {
  window.history.replaceState(null, "", "/admin/collections/posts/edit");
  const deletion = deferred<Response>();
  const fetchMock = vi.fn((path: string, options?: RequestInit) => {
    if (path === "/api/admin/session") return Promise.resolve(session());
    if (path === "/api/admin/collections") return Promise.resolve(response(200, catalog));
    if (path === "/api/admin/collections/plan") {
      return Promise.resolve(response(200, {
        baseVersion: 4,
        checksum: "posts-delete-checksum",
        definition: catalog.collections.posts,
        sql: ["DROP TABLE posts"],
      }));
    }
    if (path === "/api/admin/collections/apply" && options?.method === "POST") return deletion.promise;
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  expect(await screen.findByText("posts · physical table posts")).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Delete collection" }));
  await user.click(await screen.findByRole("button", { name: "Confirm delete" }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
    "/api/admin/collections/apply",
    expect.objectContaining({ method: "POST" }),
  ));
  const applyCall = fetchMock.mock.calls.find(([path]) => path === "/api/admin/collections/apply");
  expect(JSON.parse(String(applyCall?.[1]?.body))).toEqual({
    operation: "delete",
    name: "posts",
    checksum: "posts-delete-checksum",
    baseVersion: 4,
  });

  await act(async () => visit("/admin/collections/users/edit"));
  expect(await screen.findByText("users · physical table users")).toBeVisible();
  expect(screen.queryByText("DROP TABLE posts")).not.toBeInTheDocument();

  await act(async () => deletion.resolve(response(200, { name: "posts", deleted: true })));

  expect(window.location.pathname).toBe("/admin/collections/users/edit");
  expect(screen.getByText("users · physical table users")).toBeVisible();
});

it("does not let a completed create redirect an edit route after a new-to-edit transition", async () => {
  window.history.replaceState(null, "", "/admin/collections/new");
  const creation = deferred<Response>();
  const fetchMock = vi.fn((path: string) => {
    if (path === "/api/admin/session") return Promise.resolve(session());
    if (path === "/api/admin/collections") return Promise.resolve(response(200, catalog));
    if (path === "/api/admin/collections/plan") {
      return Promise.resolve(response(200, {
        baseVersion: 4,
        checksum: "articles-create-checksum",
        definition: {
          name: "articles",
          tableName: "articles",
          fields: [{ name: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 0 }],
        },
        sql: ["CREATE TABLE articles"],
      }));
    }
    if (path === "/api/admin/collections/apply") return creation.promise;
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  await screen.findByRole("heading", { name: "Create collection" });
  await user.type(screen.getByLabelText("Collection name"), "articles");
  await user.type(screen.getByLabelText("Field 1 name"), "title");
  await user.click(screen.getByRole("button", { name: "Preview collection" }));
  await user.click(await screen.findByRole("button", { name: "Apply collection" }));

  await act(async () => visit("/admin/collections/posts/edit"));
  expect(await screen.findByText("posts · physical table posts")).toBeVisible();
  expect(screen.queryByDisplayValue("articles")).not.toBeInTheDocument();

  await act(async () => creation.resolve(response(200, catalog.collections.posts)));

  expect(window.location.pathname).toBe("/admin/collections/posts/edit");
  expect(screen.getByText("posts · physical table posts")).toBeVisible();
});

it("resets record drafts across new and edit resources and ignores the old create result", async () => {
  const existingID = "cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa";
  window.history.replaceState(null, "", "/admin/collections/posts/records/new");
  const creation = deferred<Response>();
  const fetchMock = vi.fn((path: string, options?: RequestInit) => {
    if (path === "/api/admin/session") return Promise.resolve(session());
    if (path === "/api/admin/collections") return Promise.resolve(response(200, catalog));
    if (path === "/api/v1/collections/posts/records" && options?.method === "POST") return creation.promise;
    if (path === `/api/v1/collections/posts/records/${existingID}`) {
      return Promise.resolve(response(200, { id: existingID, title: "Existing record" }, { ETag: '"pgb1.existing"' }));
    }
    throw new Error(`Unexpected request: ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  await screen.findByRole("heading", { name: "Create posts record" });
  await user.selectOptions(await screen.findByLabelText("title state"), "value");
  await user.type(screen.getByLabelText("title"), "Unsaved new draft");
  await user.click(screen.getByRole("button", { name: "Create record" }));

  await act(async () => visit(`/admin/collections/posts/records/${existingID}`));
  expect(await screen.findByLabelText("title")).toHaveValue("Existing record");
  expect(screen.queryByDisplayValue("Unsaved new draft")).not.toBeInTheDocument();

  await act(async () => creation.resolve(response(201, { id: "new-id", title: "Unsaved new draft" })));
  expect(window.location.pathname).toBe(`/admin/collections/posts/records/${existingID}`);
  expect(screen.getByLabelText("title")).toHaveValue("Existing record");

  await act(async () => visit("/admin/collections/posts/records/new"));
  expect(await screen.findByRole("heading", { name: "Create posts record" })).toBeVisible();
  expect(await screen.findByLabelText("title state")).toHaveValue("unset");
  expect(screen.queryByLabelText("title")).not.toBeInTheDocument();
});
