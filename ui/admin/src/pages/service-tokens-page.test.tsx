import { fireEvent, screen, waitFor } from "@testing-library/react";
import { callsTo, deferredStep } from "../test/backend-script";
import { renderAdmin, scriptedAdmin } from "../test/harness";

const token = { id: "0199ab10-0000-7000-8000-000000000001", name: "worker", active: true, expiresAt: "2026-10-01T00:00:00Z", createdAt: "2026-09-01T00:00:00Z", scopes: [{ collectionId: "0199ab10-0000-7000-8000-000000000099", action: "read" as const }] };

describe("service tokens page", () => {
  it("lists, issues, reveals once, and revokes a service token", async () => {
    const issued = { ...token, id: "0199ab10-0000-7000-8000-000000000002", name: "exporter", token: "pgb_secret_once" };
    const harness = scriptedAdmin("/admin/service-tokens");
    harness.script.listServiceTokens.push({ value: [token] });
    harness.script.issueServiceToken.push({ value: issued });
    harness.script.revokeServiceToken.push({ value: undefined });
    renderAdmin(harness);
    await screen.findByText("worker");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "exporter" } });
    fireEvent.change(screen.getByLabelText("Scopes (comma separated UUID:action)"), { target: { value: "0199ab10-0000-7000-8000-000000000099:read, 0199ab10-0000-7000-8000-000000000099:update" } });
    fireEvent.click(screen.getByRole("button", { name: "Issue token" }));
    expect(await screen.findByText("pgb_secret_once")).toBeVisible();
    expect(callsTo(harness.script, "issueServiceToken")[0]?.detail.value).toEqual({ name: "exporter", ttlSeconds: 2592000, scopes: [{ collectionId: "0199ab10-0000-7000-8000-000000000099", action: "read" }, { collectionId: "0199ab10-0000-7000-8000-000000000099", action: "update" }] });
    fireEvent.click(screen.getByRole("button", { name: "I copied the token" }));
    expect(screen.queryByText("pgb_secret_once")).not.toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Revoke" })[0]);
    await waitFor(() => expect(screen.getByRole("list", { name: "Service tokens" })).toHaveTextContent("Revoked"));
  });

  it("keeps the form usable after an invalid scope", async () => {
    const harness = scriptedAdmin("/admin/service-tokens");
    harness.script.listServiceTokens.push({ value: [] });
    renderAdmin(harness);
    await screen.findByRole("list", { name: "Service tokens" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "exporter" } });
    fireEvent.change(screen.getByLabelText("Scopes (comma separated UUID:action)"), { target: { value: "bad:read" } });
    fireEvent.click(screen.getByRole("button", { name: "Issue token" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Scopes use");
    expect(screen.getByRole("button", { name: "Issue token" })).toBeEnabled();
    expect(callsTo(harness.script, "issueServiceToken")).toHaveLength(0);
  });

  it("retries a failed list", async () => {
    const harness = scriptedAdmin("/admin/service-tokens");
    harness.script.listServiceTokens.push({ fault: { kind: "unreachable" } }, { value: [] });
    renderAdmin(harness);
    await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(await screen.findByRole("list", { name: "Service tokens" })).toBeVisible();
  });

  it("ignores a list response after leaving the page", async () => {
    const pending = deferredStep<typeof token[]>();
    const harness = scriptedAdmin("/admin/service-tokens");
    harness.script.listServiceTokens.push(pending.step);
    renderAdmin(harness);
    harness.history.push("/admin/system");
    pending.settle({ value: [token] });
    expect(await screen.findByRole("heading", { name: "System" })).toBeVisible();
    expect(screen.queryByText("worker")).not.toBeInTheDocument();
  });
});
