import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { activeSessionExpiresAt } from "../test/fixtures";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const fields = [
  { name: "enabled", type: "boolean", required: true, unique: false, indexed: false, ordinal: 0 },
  { name: "count", type: "integer", required: true, unique: false, indexed: false, ordinal: 1 },
  { name: "views", type: "bigint", required: true, unique: false, indexed: false, ordinal: 2 },
  { name: "amount", type: "numeric", required: true, unique: false, indexed: false, ordinal: 3 },
  { name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 4 },
  { name: "published_at", type: "timestamptz", required: true, unique: false, indexed: false, ordinal: 5 },
  { name: "external_id", type: "uuid", required: true, unique: false, indexed: false, ordinal: 6 },
  { name: "metadata", type: "jsonb", required: true, unique: false, indexed: false, ordinal: 7 },
] as const;
const catalog = { version: 4, collections: { posts: { id: "collection", name: "posts", tableName: "posts", fields } } };

function response(status: number, body?: unknown, headers?: HeadersInit): Response {
  const responseHeaders = new Headers({ "content-type": "application/json", ...headers });
  if (status >= 200 && status < 300 && !responseHeaders.has("ETag")) responseHeaders.set("ETag", '"pgb1.test"');
  return { ok: status >= 200 && status < 300, status, headers: responseHeaders, json: vi.fn().mockResolvedValue(body) } as unknown as Response;
}

function session(): Response { return response(200, { administrator, csrfToken: "csrf-token", expiresAt: activeSessionExpiresAt() }); }

const settingsCatalog = { version: 5, collections: { settings: { id: "settings", name: "settings", tableName: "settings", fields: [
  { name: "defaulted", type: "bigint", required: false, unique: false, indexed: false, default: "7", ordinal: 0 },
  { name: "nullable_flag", type: "boolean", required: false, unique: false, indexed: false, ordinal: 1 },
  { name: "false_flag", type: "boolean", required: false, unique: false, indexed: false, ordinal: 2 },
  { name: "empty_text", type: "text", required: false, unique: false, indexed: false, ordinal: 3 },
  { name: "required_empty_text", type: "text", required: true, unique: false, indexed: false, ordinal: 4 },
] } } };

async function openSettingsCreate() {
  window.history.replaceState(null, "", "/admin/collections/settings/records/new");
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(session())
    .mockResolvedValueOnce(response(200, settingsCatalog))
    .mockResolvedValueOnce(response(201, { id: recordID }))
    .mockResolvedValueOnce(response(200, settingsCatalog))
    .mockResolvedValueOnce(response(200, { items: [] }));
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole("heading", { name: "Create settings record" });
  return { fetchMock, user };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState(null, "", "/admin/");
});

describe("record administration", () => {
  it("treats an inherited collection name as missing on the records page", async () => {
    window.history.replaceState(null, "", "/admin/collections/constructor/records");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, { version: 4, collections: {} }))
      .mockResolvedValueOnce(response(404, { error: { code: "not_found", message: "unsafe detail", details: {} } }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load records.");
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
  });

  it("treats an inherited collection name as missing on the record editor", async () => {
    window.history.replaceState(null, "", `/admin/collections/constructor/records/${recordID}`);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, { version: 4, collections: {} }))
      .mockResolvedValueOnce(response(404, { error: { code: "not_found", message: "unsafe detail", details: {} } }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load the record editor.");
    expect(screen.queryByText("This record no longer exists.")).not.toBeInTheDocument();
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
  });

  it("keeps edit actions hidden until the requested record has loaded", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    let resolveRecord: ((value: Response) => void) | undefined;
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveRecord = resolve; }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(screen.getByText("Loading record…")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete record" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("title")).not.toBeInTheDocument();

    resolveRecord?.(response(200, {
      id: recordID, enabled: false, count: 0, views: "0", amount: "0", title: "loaded",
      published_at: "2026-08-28T00:00:00Z", external_id: recordID, metadata: {},
    }));
    expect(await screen.findByLabelText("title")).toHaveValue("loaded");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Delete record" })).toBeEnabled();
  });

  it("lists first and next cursor pages and forwards applied filters and sort", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }], nextCursor: "signed cursor" }))
      .mockResolvedValueOnce(response(200, { items: [{ id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", title: "second", created_at: "2026-08-27T00:00:00Z", updated_at: "2026-08-27T00:00:00Z" }] }))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    expect(await screen.findByText("first")).toBeVisible();
    expect(screen.queryByRole("button", { name: "count" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "updated_at" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("second")).toBeVisible();
    expect(fetchMock).toHaveBeenLastCalledWith("/api/v1/collections/posts/records?limit=50&cursor=signed+cursor", expect.objectContaining({ credentials: "include" }));

    await user.type(screen.getByLabelText("Filter"), "title = 'first'");
    await user.click(screen.getByRole("button", { name: "Apply filter" }));
    expect(await screen.findByText("first")).toBeVisible();
    expect(fetchMock).toHaveBeenLastCalledWith(
      `/api/v1/collections/posts/records?limit=50&filter=title+%3D+%27first%27`,
      expect.objectContaining({ credentials: "include" }),
    );

    await user.click(screen.getByRole("button", { name: /^title( ↑)?$/ }));
    expect(fetchMock).toHaveBeenLastCalledWith(
      `/api/v1/collections/posts/records?limit=50&filter=title+%3D+%27first%27&sort=title`,
      expect.objectContaining({ credentials: "include" }),
    );
    expect(screen.getByRole("columnheader", { name: "title ↑" })).toHaveAttribute("aria-sort", "ascending");
    await user.click(screen.getByRole("button", { name: "title ↑" }));
    expect(fetchMock).toHaveBeenLastCalledWith(
      `/api/v1/collections/posts/records?limit=50&filter=title+%3D+%27first%27&sort=-title`,
      expect.objectContaining({ credentials: "include" }),
    );
    expect(screen.getByRole("columnheader", { name: "title ↓" })).toHaveAttribute("aria-sort", "descending");
  });

  it("shows a safe alert when the server rejects the filter expression and clears it after success", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }))
      .mockResolvedValueOnce(response(422, { error: { code: "validation_failed", message: "unsafe detail", details: { filter: ["unknown_field"] } } }))
      .mockResolvedValueOnce(response(200, { items: [{ id: recordID, title: "first", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" }] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    expect(await screen.findByText("first")).toBeVisible();
    await user.type(screen.getByLabelText("Filter"), "ghost = 1");
    await user.click(screen.getByRole("button", { name: "Apply filter" }));
    expect(await screen.findByText("The filter or sort expression is invalid.")).toBeVisible();
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(await screen.findByText("first")).toBeVisible();
    expect(screen.queryByText("The filter or sort expression is invalid.")).not.toBeInTheDocument();
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
    for (const field of fields) await user.selectOptions(await screen.findByLabelText(`${field.name} state`), "value");
    await user.click(await screen.findByLabelText("enabled"));
    await user.type(screen.getByLabelText("count"), "42");
    await user.type(screen.getByLabelText("views"), "9223372036854775807");
    await user.type(screen.getByLabelText("amount"), "12345678901234567890.12345678901234567890");
    await user.type(screen.getByLabelText("title"), "hello");
    await user.type(screen.getByLabelText("published_at"), "2026-08-28T12:34:56Z");
    await user.type(screen.getByLabelText("external_id"), "ABCDEFAB-CDEF-0ABC-0DEF-ABCDEFABCDEF");
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
      external_id: "abcdefab-cdef-0abc-0def-abcdefabcdef",
      metadata: { tags: ["safe"], rank: 2 },
    });
    const requestHeaders = new Headers(options.headers);
    expect(requestHeaders.get("X-PgBase-CSRF")).toBe("csrf-token");
    expect(requestHeaders.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("reuses a create key after ambiguous failures and rotates it when the input changes", async () => {
    window.history.replaceState(null, "", "/admin/collections/posts/records/new");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(response(201, { id: recordID }))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("title state"), "value");
    await user.type(screen.getByLabelText("title"), "first attempt");
    await user.click(screen.getByRole("button", { name: "Create record" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to save the record.");
    await waitFor(() => expect(screen.getByRole("button", { name: "Create record" })).toBeEnabled());

    await user.click(screen.getByRole("button", { name: "Create record" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4));
    await waitFor(() => expect(screen.getByRole("button", { name: "Create record" })).toBeEnabled());

    const firstRequest = fetchMock.mock.calls[2][1] as RequestInit;
    const retryRequest = fetchMock.mock.calls[3][1] as RequestInit;
    const firstKey = new Headers(firstRequest.headers).get("Idempotency-Key");
    expect(retryRequest.body).toBe(firstRequest.body);
    expect(new Headers(retryRequest.headers).get("Idempotency-Key")).toBe(firstKey);

    await user.clear(screen.getByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "changed attempt");
    await user.click(screen.getByRole("button", { name: "Create record" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(7));

    const changedRequest = fetchMock.mock.calls[4][1] as RequestInit;
    expect(changedRequest.body).not.toBe(firstRequest.body);
    expect(new Headers(changedRequest.headers).get("Idempotency-Key")).not.toBe(firstKey);
  });

  it("omits untouched fields so database defaults can apply", async () => {
    const { fetchMock, user } = await openSettingsCreate();
    expect(await screen.findByLabelText("defaulted state")).toHaveValue("unset");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({});
  });

  it("distinguishes a nullable boolean from a concrete false boolean", async () => {
    const { fetchMock, user } = await openSettingsCreate();
    await user.selectOptions(await screen.findByLabelText("nullable_flag state"), "null");
    await user.selectOptions(screen.getByLabelText("false_flag state"), "value");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({ nullable_flag: null, false_flag: false });
  });

  it("submits optional and required empty text as concrete strings instead of null", async () => {
    const { fetchMock, user } = await openSettingsCreate();
    await user.selectOptions(await screen.findByLabelText("empty_text state"), "value");
    await user.selectOptions(await screen.findByLabelText("required_empty_text state"), "value");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({ empty_text: "", required_empty_text: "" });
  });

	it("accepts nil and mixed-case canonical UUID values and normalizes case", async () => {
    window.history.replaceState(null, "", "/admin/collections/ids/records/new");
    const idsCatalog = { version: 6, collections: { ids: { id: "ids", name: "ids", tableName: "ids", fields: [
      { name: "nil_id", type: "uuid", required: true, unique: false, indexed: false, ordinal: 0 },
      { name: "mixed_id", type: "uuid", required: true, unique: false, indexed: false, ordinal: 1 },
    ] } } };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, idsCatalog))
      .mockResolvedValueOnce(response(201, { id: recordID }))
      .mockResolvedValueOnce(response(200, idsCatalog))
      .mockResolvedValueOnce(response(200, { items: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await user.selectOptions(await screen.findByLabelText("nil_id state"), "value");
    await user.selectOptions(screen.getByLabelText("mixed_id state"), "value");
    await user.type(screen.getByLabelText("nil_id"), "00000000-0000-0000-0000-000000000000");
    await user.type(screen.getByLabelText("mixed_id"), "ABCDEFAB-CDEF-0ABC-0DEF-ABCDEFABCDEF");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    const [, options] = fetchMock.mock.calls[2] as [string, RequestInit];
    expect(JSON.parse(options.body as string)).toEqual({
      nil_id: "00000000-0000-0000-0000-000000000000",
      mixed_id: "abcdefab-cdef-0abc-0def-abcdefabcdef",
	});
	});

	it("rejects integers outside the PostgreSQL integer range before sending them", async () => {
		window.history.replaceState(null, "", "/admin/collections/posts/records/new");
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(session())
			.mockResolvedValueOnce(response(200, catalog));
		vi.stubGlobal("fetch", fetchMock);
		const user = userEvent.setup();
		render(<App />);

		await user.selectOptions(await screen.findByLabelText("count state"), "value");
		const input = screen.getByLabelText("count");
		await user.type(input, "2147483648");
		await user.click(screen.getByRole("button", { name: "Create record" }));
		expect(await screen.findByText("Value does not match the field type.")).toBeVisible();
		expect(fetchMock).toHaveBeenCalledTimes(2);

		await user.clear(input);
		await user.type(input, "-2147483649");
		await user.click(screen.getByRole("button", { name: "Create record" }));
		expect(fetchMock).toHaveBeenCalledTimes(2);
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
    expect(await screen.findByLabelText("views")).toHaveValue("9223372036854775807");
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
    expect(new Headers(options.headers).get("If-Match")).toBe('"pgb1.test"');
  });

  it("keeps a stale draft intact and requires an explicit reload after a 412", async () => {
    window.history.replaceState(null, "", `/admin/collections/posts/records/${recordID}`);
    const original = {
      id: recordID, enabled: true, count: 1, views: "2", amount: "3", title: "original",
      published_at: "2026-08-28T12:34:56Z", external_id: recordID, metadata: {},
      created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z",
    };
    const latest = { ...original, title: "latest from server", updated_at: "2026-08-28T00:01:00Z" };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, original, { ETag: '"pgb1.old"' }))
      .mockResolvedValueOnce(response(412, { error: { code: "precondition_failed", message: "unsafe detail", details: {} } }, { ETag: '"pgb1.current"' }))
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(response(200, catalog))
      .mockResolvedValueOnce(response(200, latest, { ETag: '"pgb1.current"' }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    const title = await screen.findByLabelText("title");
    await user.clear(title);
    await user.type(title, "my unsaved edit");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Reload the latest version before saving or deleting.");
    expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
    expect(screen.getByLabelText("title")).toHaveValue("my unsaved edit");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete record" })).toBeDisabled();
    expect(fetchMock).toHaveBeenCalledTimes(4);
    const [, updateOptions] = fetchMock.mock.calls[3] as [string, RequestInit];
    expect(new Headers(updateOptions.headers).get("If-Match")).toBe('"pgb1.old"');

    await user.click(screen.getByRole("button", { name: "Reload record" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Your draft was preserved; try again.");
    expect(screen.getByLabelText("title")).toHaveValue("my unsaved edit");
    expect(screen.getByRole("button", { name: "Reload record" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete record" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Reload record" }));
    await waitFor(() => expect(screen.getByLabelText("title")).toHaveValue("latest from server"));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Delete record" })).toBeEnabled();
    expect(fetchMock).toHaveBeenCalledTimes(7);
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
    expect(new Headers(deleteOptions.headers).get("If-Match")).toBe('"pgb1.test"');
  });
});
