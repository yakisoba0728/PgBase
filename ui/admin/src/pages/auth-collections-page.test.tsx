import { fireEvent, screen, within } from "@testing-library/react";
import { callsTo } from "../test/backend-script";
import { renderAdmin, scriptedAdmin } from "../test/harness";

const users = { id: "0199ab10-0000-7000-8000-000000000001", name: "users", registrationEnabled: true, profileSchema: { displayName: "text" } };
const members = { id: "0199ab10-0000-7000-8000-000000000002", name: "members", registrationEnabled: false, profileSchema: { nickname: "text" } };

describe("auth collections page", () => {
  it("creates a realm and saves its registration and scalar profile schema", async () => {
    const created = { ...members, id: "0199ab10-0000-7000-8000-000000000003", name: "writers" };
    const updated = { ...members, registrationEnabled: true, profileSchema: { nickname: "text", bio: "text" } };
    const harness = scriptedAdmin("/admin/auth-collections");
    harness.script.listAuthCollections.push({ value: [members] });
    harness.script.createAuthCollection.push({ value: created });
    harness.script.updateAuthCollection.push({ value: updated });
    renderAdmin(harness);
    await screen.findByText("members");
    fireEvent.change(screen.getByLabelText("Auth collection name"), { target: { value: "writers" } });
    fireEvent.change(screen.getByLabelText("New profile schema"), { target: { value: '{"penName":"text"}' } });
    fireEvent.click(screen.getByRole("button", { name: "Create auth collection" }));
    expect(await screen.findByText("writers")).toBeVisible();
    expect(callsTo(harness.script, "createAuthCollection")[0]?.detail.value).toMatchObject({ name: "writers", registrationEnabled: false, profileSchema: { penName: "text" } });
    fireEvent.change(screen.getByLabelText("members profile schema"), { target: { value: '{"nickname":"text","bio":"text"}' } });
    const memberItem = screen.getByText("members").closest("li");
    if (memberItem === null) throw new Error("members row was not rendered");
    fireEvent.click(within(memberItem).getByRole("checkbox", { name: "Registration enabled" }));
    expect(callsTo(harness.script, "updateAuthCollection")[0]?.detail).toEqual({ id: members.id, value: { registrationEnabled: true, profileSchema: { nickname: "text", bio: "text" } } });
  });

  it("keeps the default realm and reports a populated realm deletion failure", async () => {
    const harness = scriptedAdmin("/admin/auth-collections");
    harness.script.listAuthCollections.push({ value: [users, members] });
    harness.script.deleteAuthCollection.push({ fault: { kind: "referenced" } });
    renderAdmin(harness);
    await screen.findByText("The default users realm cannot be deleted.");
    const deletes = screen.getAllByRole("button", { name: "Delete" });
    expect(deletes[0]).toBeDisabled();
    fireEvent.click(deletes[1]);
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to save auth collection");
    expect(screen.getByText("members")).toBeVisible();
  });

  it("retries a failed initial load and rejects an invalid profile schema before a request", async () => {
    const harness = scriptedAdmin("/admin/auth-collections");
    harness.script.listAuthCollections.push({ fault: { kind: "unreachable" } }, { value: [] });
    renderAdmin(harness);
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(await screen.findByRole("list", { name: "Auth collections" })).toBeVisible();
    fireEvent.change(screen.getByLabelText("Auth collection name"), { target: { value: "writers" } });
    fireEvent.change(screen.getByLabelText("New profile schema"), { target: { value: "[]" } });
    fireEvent.click(screen.getByRole("button", { name: "Create auth collection" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Profile schema must be a JSON object");
    expect(callsTo(harness.script, "createAuthCollection")).toHaveLength(0);
  });

});
