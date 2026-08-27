import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const fields = [
  { name: "enabled", type: "boolean", required: true, unique: false, indexed: false, ordinal: 0 },
  { name: "count", type: "integer", required: true, unique: false, indexed: false, ordinal: 1 },
  { name: "views", type: "bigint", required: true, unique: false, indexed: false, ordinal: 2 },
  { name: "amount", type: "numeric", required: true, unique: false, indexed: false, ordinal: 3 },
  { name: "title", type: "text", required: true, unique: false, indexed: false, ordinal: 4 },
  { name: "published_at", type: "timestamptz", required: true, unique: false, indexed: false, ordinal: 5 },
  { name: "external_id", type: "uuid", required: true, unique: false, indexed: false, ordinal: 6 },
  { name: "metadata", type: "jsonb", required: true, unique: false, indexed: false, ordinal: 7 },
] as const;
const catalog = { version: 4, collections: { posts: { id: "collection", name: "posts", tableName: "posts", fields } } };

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers({ "content-type": "application/json" }), json: vi.fn().mockResolvedValue(body) } as unknown as Response;
}

function session(): Response { return response(200, { administrator, csrfToken: "csrf-token", expiresAt: "2026-08-29T00:00:00Z" }); }

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/admin/");
});

describe("record administration", () => {
  it("lists first and next cursor pages without arbitrary filters", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }], nextCursor: "signed cursor" }))
      .mockResolvedValueOnce(response(200, { items: [{ id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", title: "second", created_at: "2026-08-27T00:00:00Z", updated_at: "2026-08-27T00:00:00Z" }] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    expect(await screen.findByText("first")).toBeVisible();
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("second")).toBeVisible();
    expect(fetchMock).toHaveBeenLastCalledWith("/api/v1/collections/posts/records?limit=50&cursor=signed+cursor", expect.objectContaining({ credentials: "include" }));
  });

  it("creates all eight JSON representations without rounding decimal strings", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(201, { id: recordID }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Create posts record" });
    await user.click(await screen.findByLabelText("enabled"));
    await user.type(screen.getByLabelText("count"), "42");
    await user.type(screen.getByLabelText("views"), "9223372036854775807");
    await user.type(screen.getByLabelText("amount"), "12345678901234567890.12345678901234567890");
    await user.type(screen.getByLabelText("title"), "hello");
    await user.type(screen.getByLabelText("published_at"), "2026-08-28T12:34:56Z");
    await user.type(screen.getByLabelText("external_id"), "12345678-1234-4567-8901-123456789ABC");
    fireEvent.change(screen.getByLabelText("metadata"), { target: { value: "{\"tags\":[\"safe\"],\"rank\":2}" } });
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [path, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(path).toBe("/api/v1/collections/posts/records");
    expect(JSON.parse(options.body as string)).toEqual({
      enabled: true,
      count: 42,
      views: "9223372036854775807",
      amount: "12345678901234567890.12345678901234567890",
      title: "hello",
      published_at: "2026-08-28T12:34:56.000Z",
      external_id: "12345678-1234-4567-8901-123456789abc",
      metadata: { tags: ["safe"], rank: 2 },
    });
    expect(new Headers(options.headers).get("X-PgBase-CSRF")).toBe("csrf-token");
  });

  it("validates JSONB locally, sends partial updates, and shows safe 422 field errors", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const record = {
      id: recordID, enabled: true, count: 1, views: "9223372036854775807", amount: "1.2300", title: "old",
      published_at: "2026-08-28T12:34:56Z", external_id: "12345678-1234-4567-8901-123456789abc", metadata: { old: true },
      created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z",
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, record))
      .mockResolvedValueOnce(response(422, { error: { code: "validation_failed", message: "unsafe SQL detail", details: { amount: ["type_mismatch"] } } }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Edit posts record" });
    expect(screen.getByLabelText("views")).toHaveValue("9223372036854775807");
    expect(screen.getByLabelText("amount")).toHaveValue("1.2300");
    await user.clear(screen.getByLabelText("metadata"));
    await user.type(screen.getByLabelText("metadata"), "not json");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Enter valid JSON.")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(3);

    await user.clear(screen.getByLabelText("metadata"));
    fireEvent.change(screen.getByLabelText("metadata"), { target: { value: "{\n  \"old\": true\n}" } });
    await user.clear(screen.getByLabelText("amount"));
    await user.type(screen.getByLabelText("amount"), "99999999999999999999.0001");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText("Value does not match the field type.")).toBeVisible();
    expect(screen.queryByText("unsafe SQL detail")).not.toBeInTheDocument();
    const [, options] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({ amount: "99999999999999999999.0001" });
  });

  it("confirms deletion and handles a record deleted by another administrator", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(404, { error: { code: "not_found", message: "unsafe detail", details: {} } }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("This record no longer exists.");
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
  });

  it("does not delete until the administrator confirms", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { id: recordID, enabled: false, count: 0, views: "0", amount: "0", title: "old", published_at: "2026-08-28T00:00:00Z", external_id: recordID, metadata: {} }))
      .mockResolvedValueOnce(response(204))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByLabelText("title");
    await user.click(screen.getByRole("button", { name: "Delete record" }));
    expect(fetchMock).toHaveBeenCalledTimes(3);
    await user.click(screen.getByRole("button", { name: "Delete record" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(6));
    expect(confirmMock).toHaveBeenCalledTimes(2);
    const [deletePath, deleteOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(deletePath).toBe(`/api/v1/collections/posts/records/${recordID}`);
    expect(deleteOptions.method).toBe("DELETE");
  });
});
