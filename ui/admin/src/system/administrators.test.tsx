import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { setLanguage } from "../i18n";
import { AdministratorsView } from "./AdministratorsView";

const api = vi.hoisted(() => ({ listAdministrators: vi.fn(), createAdministrator: vi.fn(), setAdministratorActive: vi.fn(), resetAdministratorPassword: vi.fn(), revokeAdministratorSessions: vi.fn(), getSession: vi.fn() }));

vi.mock("../api/system", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/system")>()),
  listAdministrators: api.listAdministrators,
  createAdministrator: api.createAdministrator,
  setAdministratorActive: api.setAdministratorActive,
  resetAdministratorPassword: api.resetAdministratorPassword,
  revokeAdministratorSessions: api.revokeAdministratorSessions,
}));
vi.mock("../api/session", async (importOriginal) => ({ ...(await importOriginal<typeof import("../api/session")>()), getSession: api.getSession }));
vi.mock("../app/signout", () => ({ useLeaveSession: () => vi.fn() }));

const at = "2026-11-13T00:00:00Z";
const owner = { id: "a1", email: "owner@example.test", active: true, createdAt: at, updatedAt: at };
const other = { id: "a2", email: "other@example.test", active: true, createdAt: at, updatedAt: at };
const wrong = () => new ApiError(403, "current_password_invalid", "wrong");

function renderAt(admin: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/administrators?admin=${admin}`]}>
        <AdministratorsView />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  setLanguage("en");
  vi.clearAllMocks();
  api.listAdministrators.mockResolvedValue({ items: [owner, other] });
  api.getSession.mockResolvedValue({ administrator: owner, expiresAt: at, csrfToken: "csrf" });
});

describe("administrator security changes ask for your current password", () => {
  it("creates an administrator with your current password and maps a wrong one to that field", async () => {
    const user = userEvent.setup();
    api.createAdministrator.mockRejectedValueOnce(wrong()).mockResolvedValueOnce({ ...other, id: "a3", email: "third@example.test" });
    renderAt("new");
    const current = await screen.findByLabelText(/Current password/);
    expect(current).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute("autocomplete", "new-password");
    await user.type(screen.getByRole("textbox", { name: /Email/ }), "third@example.test");
    await user.type(screen.getByLabelText(/^Password/), "third administrator password");
    await user.type(current, "wrong password");
    await user.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(current).toHaveAccessibleDescription("The current password is incorrect."));
    expect(api.createAdministrator.mock.calls[0][0]).toEqual({ currentPassword: "wrong password", email: "third@example.test", password: "third administrator password" });
  });

  it("disables only after the current password, keeping the dialog open on a wrong one", async () => {
    const user = userEvent.setup();
    api.setAdministratorActive.mockRejectedValueOnce(wrong()).mockResolvedValueOnce({ ...other, active: false });
    renderAt(other.id);
    await user.click(await screen.findByRole("button", { name: "Disable" }));
    const dialog = within(screen.getByRole("dialog"));
    const confirm = dialog.getByRole("button", { name: "Disable" });
    expect(confirm).toBeDisabled();
    const current = dialog.getByLabelText(/Current password/);
    expect(current).toHaveAttribute("autocomplete", "current-password");
    await user.type(current, "wrong password");
    await user.click(confirm);
    await waitFor(() => expect(current).toHaveAccessibleDescription("The current password is incorrect."));
    expect(api.setAdministratorActive).toHaveBeenCalledWith(other.id, false, "wrong password");
    await user.clear(current);
    await user.type(current, "right password");
    await user.click(confirm);
    await waitFor(() => expect(api.setAdministratorActive).toHaveBeenLastCalledWith(other.id, false, "right password"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("resets another administrator's password with your current password", async () => {
    const user = userEvent.setup();
    api.resetAdministratorPassword.mockResolvedValue(undefined);
    renderAt(other.id);
    await user.click(await screen.findByRole("button", { name: "Set password" }));
    const dialog = within(screen.getByRole("dialog"));
    await user.type(dialog.getByLabelText(/Current password/), "right password");
    await user.type(dialog.getByLabelText(/New password/), "replacement password 1");
    await user.type(dialog.getByLabelText(/Repeat password/), "replacement password 1");
    await user.click(dialog.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(api.resetAdministratorPassword).toHaveBeenCalledWith(other.id, "replacement password 1", "right password"));
  });
});

it("ends another administrator's sessions only after their email is typed", async () => {
  const user = userEvent.setup();
  api.revokeAdministratorSessions.mockResolvedValue(undefined);
  renderAt(other.id);
  await user.click(await screen.findByRole("button", { name: "End sessions" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "End sessions" });
  expect(confirm).toBeDisabled();
  await user.type(dialog.getByRole("textbox", { name: `Type ${other.email} to confirm` }), other.email);
  await user.click(confirm);
  await waitFor(() => expect(api.revokeAdministratorSessions).toHaveBeenCalledWith(other.id));
});

describe("a deep-linked administrator", () => {
  const far = { id: "0000000a-0000-4000-8000-000000000009", email: "far@example.test", active: true, createdAt: at, updatedAt: at };

  it("opens one beyond the loaded page by reading it on its own", async () => {
    api.listAdministrators.mockImplementation(async (params: { limit?: number; after?: string }) =>
      params.limit === 1 ? { items: [far] } : { items: [owner, other], nextAfter: other.id });
    renderAt(far.id);
    expect(await screen.findByRole("heading", { name: far.email })).toBeInTheDocument();
    expect(api.listAdministrators).toHaveBeenCalledWith({ limit: 1, after: "0000000a-0000-4000-8000-000000000008" }, expect.anything());
  });

  it("says so when there is no such administrator", async () => {
    api.listAdministrators.mockImplementation(async (params: { limit?: number }) => (params.limit === 1 ? { items: [] } : { items: [owner, other] }));
    renderAt(far.id);
    expect(await screen.findByText("Not found.")).toBeInTheDocument();
  });
});

describe("the administrator list window", () => {
  it("drops the oldest pages past ten and keeps an opened administrator's pane mounted", async () => {
    const user = userEvent.setup();
    const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
    const admin = (n: number) => ({ ...other, id: uuid(n), email: `admin${n}@example.test` });
    api.listAdministrators.mockImplementation(async ({ limit, after }: { limit: number; after?: string }) => {
      if (limit === 1) return { items: [admin(Number(after!.slice(-12)) + 1)] };
      const page = after === undefined ? 0 : Number(after.slice(-12)) / 5;
      return { items: Array.from({ length: 5 }, (_, i) => admin(page * 5 + i + 1)), nextAfter: uuid(page * 5 + 5) };
    });
    // Opened from the list, so no lookup of its own has run yet.
    renderAt("");
    await user.click(await screen.findByText("admin1@example.test", { selector: "td" }));
    const pane = await screen.findByRole("region", { name: "Administrator" });
    const title = await within(pane).findByRole("heading", { name: "admin1@example.test" });
    for (let page = 1; page <= 10; page++) {
      await user.click(await screen.findByRole("button", { name: "Load more" }));
      await screen.findByText(`admin${page * 5 + 1}@example.test`, { selector: "td" });
    }
    await waitFor(() => expect(screen.queryByText("admin1@example.test", { selector: "td" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Back to start" })).toBeInTheDocument();
    // Read on its own once it left the list, without remounting the pane.
    await waitFor(() => expect(api.listAdministrators).toHaveBeenCalledWith({ limit: 1, after: uuid(0) }, expect.anything()));
    expect(within(pane).getByRole("heading", { name: "admin1@example.test" })).toBe(title);
  });
});
