import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { callsTo } from "../test/backend-script";
import { renderAdmin, scriptedAdmin } from "../test/harness";

describe("collection designer return navigation", () => {
  it("returns an untouched new collection to the list from the keyboard without prompting", async () => {
    const harness = scriptedAdmin("/admin/collections/new");
    harness.script.catalog.push({ value: { version: 0, collections: [] } }, { value: { version: 0, collections: [] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByLabelText("Collection name");
    const back = screen.getByRole("link", { name: "Back to collections" });
    back.focus();
    await user.keyboard("{Enter}");

    await waitFor(() => expect(harness.history.path()).toBe("/admin/collections"));
    expect(confirm).not.toHaveBeenCalled();
    expect(callsTo(harness.script, "plan")).toHaveLength(0);
    expect(callsTo(harness.script, "apply")).toHaveLength(0);
  });

  it("preserves the draft when return is cancelled, and leaves only after confirmation", async () => {
    const harness = scriptedAdmin("/admin/collections/new");
    harness.script.catalog.push({ value: { version: 0, collections: [] } }, { value: { version: 0, collections: [] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.type(await screen.findByLabelText("Collection name"), "posts");
    await user.click(screen.getByRole("link", { name: "Back to collections" }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(harness.history.path()).toBe("/admin/collections/new");
    expect(screen.getByLabelText("Collection name")).toHaveValue("posts");

    confirm.mockReturnValue(true);
    await user.click(screen.getByRole("link", { name: "Back to collections" }));
    await waitFor(() => expect(harness.history.path()).toBe("/admin/collections"));
    expect(callsTo(harness.script, "plan")).toHaveLength(0);
    expect(callsTo(harness.script, "apply")).toHaveLength(0);
  });

  it("offers a return path even when the requested collection does not exist", async () => {
    const harness = scriptedAdmin("/admin/collections/missing/edit");
    harness.script.catalog.push({ value: { version: 0, collections: [] } }, { value: { version: 0, collections: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Collection not found.");
    await user.click(screen.getByRole("link", { name: "Back to collections" }));
    await waitFor(() => expect(harness.history.path()).toBe("/admin/collections"));
  });
});
