import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { setLanguage } from "../i18n";
import { ProfileView } from "./ProfileView";

const api = vi.hoisted(() => ({ getProfile: vi.fn(), getIpAllowlist: vi.fn(), updateProfile: vi.fn(), updateIpAllowlist: vi.fn(), leave: vi.fn() }));

vi.mock("../api/session", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/session")>()),
  getProfile: api.getProfile,
  getIpAllowlist: api.getIpAllowlist,
  updateProfile: api.updateProfile,
  updateIpAllowlist: api.updateIpAllowlist,
}));
vi.mock("../app/signout", () => ({ useLeaveSession: () => api.leave }));

const profile = { id: "a1", email: "owner@example.test", active: true, createdAt: "2026-11-13T00:00:00Z", updatedAt: "2026-11-13T00:00:00Z" };

/** Several sections share button names, so queries stay inside the control's own form. */
const formOf = (control: HTMLElement) => within(control.closest("form")!);

function renderProfile() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><ProfileView /></QueryClientProvider>);
}

beforeEach(() => {
  setLanguage("en");
  vi.clearAllMocks();
  api.getProfile.mockResolvedValue(profile);
  api.getIpAllowlist.mockResolvedValue({ prefixes: [], clientAddress: "203.0.113.9" });
});

describe("profile IP allowlist", () => {
  it("shows the server's view of this address and blocks a list that leaves it out", async () => {
    const user = userEvent.setup();
    renderProfile();
    expect(await screen.findByText("203.0.113.9")).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent(/locked out/);
    const field = screen.getByLabelText(/Networks/);
    await user.type(field, "198.51.100.0/24");
    expect(field).toHaveAccessibleDescription(/203\.0\.113\.9 is not inside this list/);
    expect(formOf(field).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(api.updateIpAllowlist).not.toHaveBeenCalled();
  });

  it("saves a covering list after confirmation and keeps the CLI recovery hint", async () => {
    const user = userEvent.setup();
    api.updateIpAllowlist.mockResolvedValue({ prefixes: ["203.0.113.0/24"], clientAddress: "203.0.113.9" });
    renderProfile();
    const field = await screen.findByLabelText(/Networks/);
    await user.type(field, "203.0.113.0/24");
    await user.click(formOf(field).getAllByRole("button", { name: "Save" })[0]);
    expect(screen.getByText(/pgbase admin ip deny --email owner@example\.test/)).toBeInTheDocument();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.updateIpAllowlist).toHaveBeenCalledWith(["203.0.113.0/24"]));
  });

  it("maps the server's refusal to the field", async () => {
    const user = userEvent.setup();
    // The server decides even when the client-side mirror lets the list through.
    api.updateIpAllowlist.mockRejectedValue(new ApiError(409, "ip_allowlist_excludes_caller", "excluded"));
    renderProfile();
    const field = await screen.findByLabelText(/Networks/);
    await user.type(field, "203.0.113.0/24");
    await user.click(formOf(field).getAllByRole("button", { name: "Save" })[0]);
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(field).toHaveAccessibleDescription(/is not inside this list/));
  });
});

describe("profile credentials", () => {
  it("asks for the current password before changing the password and maps a wrong one to that field", async () => {
    const user = userEvent.setup();
    api.updateProfile.mockRejectedValueOnce(new ApiError(403, "current_password_invalid", "wrong")).mockResolvedValueOnce(profile);
    renderProfile();
    const newPassword = await screen.findByLabelText(/New password/);
    const form = formOf(newPassword);
    const current = form.getByLabelText(/Current password/);
    expect(current).toHaveAttribute("autocomplete", "current-password");
    expect(newPassword).toHaveAttribute("autocomplete", "new-password");
    await user.type(current, "wrong password here");
    await user.type(newPassword, "replacement password 1");
    await user.type(form.getByLabelText(/Repeat password/), "replacement password 1");
    await user.click(form.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(current).toHaveAccessibleDescription("The current password is incorrect."));
    expect(api.updateProfile).toHaveBeenCalledWith({ currentPassword: "wrong password here", newPassword: "replacement password 1" });
    expect(api.leave).not.toHaveBeenCalled();
    await user.click(form.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(api.leave).toHaveBeenCalled());
  });

  it("sends the current password with an email change", async () => {
    const user = userEvent.setup();
    api.updateProfile.mockResolvedValue({ ...profile, email: "renamed@example.test" });
    renderProfile();
    const email = await screen.findByRole("textbox", { name: /Email/ });
    const form = formOf(email);
    await user.clear(email);
    await user.type(email, "renamed@example.test");
    const save = form.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    const current = form.getByLabelText(/Current password/);
    expect(current).toHaveAttribute("autocomplete", "current-password");
    await user.type(current, "correct password");
    await user.click(save);
    await waitFor(() => expect(api.updateProfile).toHaveBeenCalledWith({ currentPassword: "correct password", email: "renamed@example.test" }));
  });
});
