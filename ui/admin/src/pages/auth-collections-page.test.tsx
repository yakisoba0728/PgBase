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

  it("keeps a stored SMTP password redacted while saving settings and using the local test action", async () => {
    const smtp = { address: "localhost:1025", from: "mailer@example.test", username: "mailer", passwordConfigured: true, serverName: "localhost", implicitTls: false, allowInsecureLoopback: true };
    const harness = scriptedAdmin("/admin/auth-collections");
    harness.script.listAuthCollections.push({ value: [users] });
    harness.script.getAuthAdministration.push({ value: { policy: { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 72, passwordPattern: "", identityFields: ["email"], mfaDurationSeconds: 1800, mfaRule: "", mfaEnabled: true, otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 }, providers: [], templates: [], smtp } });
    harness.script.putAuthSMTP.push({ value: smtp });
    harness.script.testAuthSMTP.push({ value: undefined });
    renderAdmin(harness);
    await screen.findByText("users");
    fireEvent.click(await screen.findByText("Authentication settings"));
    await screen.findByLabelText("Server address");
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveAttribute("placeholder", "Stored; leave blank to keep");
    fireEvent.change(screen.getByLabelText("Server address"), { target: { value: "localhost:2525" } });
    fireEvent.click(screen.getByRole("button", { name: "Save SMTP" }));
    expect(await screen.findByText("SMTP settings saved. Stored passwords are never returned.")).toBeVisible();
    expect(callsTo(harness.script, "putAuthSMTP")[0]?.detail.value).toMatchObject({ ...smtp, address: "localhost:2525" });
    expect(callsTo(harness.script, "putAuthSMTP")[0]?.detail.value).not.toHaveProperty("password");
    fireEvent.change(screen.getByLabelText("Local test recipient"), { target: { value: "sink@example.test" } });
    fireEvent.click(screen.getByRole("button", { name: "Send test" }));
    expect(await screen.findByText("Test message sent to sink@example.test.")).toBeVisible();
    expect(callsTo(harness.script, "testAuthSMTP")[0]?.detail).toEqual({ id: users.id, recipient: "sink@example.test" });
  });

  it("replaces a mail-template draft after a normalized save and a settings reload", async () => {
    const policy = { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 72, passwordPattern: "", identityFields: ["email"], mfaDurationSeconds: 1800, mfaRule: "", mfaEnabled: true, otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 };
    const initial = { purpose: "verify" as const, subject: "Initial subject", body: "Initial body", htmlBody: "<p>Initial</p>" };
    const normalized = { ...initial, subject: "Normalized subject", body: "Normalized body", htmlBody: "<p>Normalized</p>" };
    const reloaded = { ...initial, subject: "Reloaded subject", body: "Reloaded body", htmlBody: "<p>Reloaded</p>" };
    const harness = scriptedAdmin("/admin/auth-collections");
    harness.script.listAuthCollections.push({ value: [users] });
    harness.script.getAuthAdministration.push(
      { value: { policy, providers: [], templates: [initial] } },
      { value: { policy, providers: [], templates: [reloaded] } },
    );
    harness.script.putAuthMailTemplate.push({ value: normalized });
    renderAdmin(harness);
    fireEvent.click(await screen.findByText("Authentication settings"));
    const subject = (await screen.findAllByLabelText("Subject"))[0];
    fireEvent.change(subject, { target: { value: "Edited subject" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Save template" })[0]);
    expect(await screen.findByDisplayValue("Normalized subject")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Reload authentication settings" }));
    expect(await screen.findByDisplayValue("Reloaded subject")).toHaveValue(
      "Reloaded subject",
    );
  });

});
