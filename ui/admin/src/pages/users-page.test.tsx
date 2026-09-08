import { act, fireEvent, screen } from "@testing-library/react";
import { vi } from "vitest";
import { callsTo, deferredStep } from "../test/backend-script";
import { renderAdmin, scriptedAdmin } from "../test/harness";

const user = { id: "0199ab10-0000-7000-8000-000000000001", email: "owner@example.test", active: true, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z" };

describe("users page", () => {
  it("lists bounded users and deactivates only after confirmation", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const harness = scriptedAdmin("/admin/users");
    harness.script.users.push({ value: { items: [user] } });
    harness.script.setUserActive.push({ value: { ...user, active: false } });
    renderAdmin(harness);
    await screen.findByText(user.email);
    fireEvent.click(screen.getByRole("button", { name: "Deactivate" }));
    await screen.findByText("Inactive");
    expect(callsTo(harness.script, "setUserActive")[0]?.detail).toEqual({ id: user.id, active: false });
  });

  it("retries an unavailable user list", async () => {
    const harness = scriptedAdmin("/admin/users");
    harness.script.users.push({ fault: { kind: "unreachable" } }, { value: { items: [] } });
    renderAdmin(harness);
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await screen.findByText("No public users yet.");
  });

  it("blocks every user and page control during a mutation, then permits retry", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const other = { ...user, id: "0199ab10-0000-7000-8000-000000000002", email: "other@example.test" };
    const pending = deferredStep<typeof user>();
    const harness = scriptedAdmin("/admin/users");
    harness.script.users.push({ value: { items: [user, other], nextAfter: other.id } });
    harness.script.setUserActive.push(pending.step, { value: { ...user, active: false } });
    renderAdmin(harness);
    await screen.findByText(other.email);
    fireEvent.click(screen.getAllByRole("button", { name: "Deactivate" })[0]);
    expect(screen.getAllByRole("button", { name: "Deactivate" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Revoke sessions" })[1]).toBeDisabled();
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
    await act(async () => pending.settle({ fault: { kind: "unreachable" } }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to update user");
    harness.script.setUserActive.push({ value: { ...user, active: false } });
    fireEvent.click(screen.getAllByRole("button", { name: "Deactivate" })[0]);
    expect(await screen.findByText("Inactive")).toBeVisible();
  });

  it("requests the bounded next page using its cursor", async () => {
    const next = { ...user, id: "0199ab10-0000-7000-8000-000000000003", email: "next@example.test" };
    const harness = scriptedAdmin("/admin/users");
    harness.script.users.push({ value: { items: [user], nextAfter: user.id } }, { value: { items: [next] } });
    renderAdmin(harness);
    await screen.findByText(user.email);
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText(next.email)).toBeVisible();
    expect(callsTo(harness.script, "users")[1]?.detail).toEqual({ after: user.id });
  });

  it("does not let an old mount overwrite the users page after navigation returns", async () => {
    const old = deferredStep<{ items: typeof user[] }>();
    const fresh = { ...user, id: "0199ab10-0000-7000-8000-000000000004", email: "fresh@example.test" };
    const harness = scriptedAdmin("/admin/users");
    harness.script.users.push(old.step, { value: { items: [fresh] } });
    renderAdmin(harness);
    expect(await screen.findByText("Loading users…")).toBeVisible();
    await act(async () => { harness.history.push("/admin/system"); });
    await act(async () => { harness.history.push("/admin/users"); });
    expect(await screen.findByText(fresh.email)).toBeVisible();
    await act(async () => old.settle({ value: { items: [user] } }));
    expect(screen.getByText(fresh.email)).toBeVisible();
    expect(screen.queryByText(user.email)).not.toBeInTheDocument();
  });
});
