import { act, render, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { UnsavedDrafts, draftLedger, useUnsavedDraft } from "./unsaved-draft";

function unloading(): Event {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event;
}

describe("unsaved drafts", () => {
  it("asks only while a resource still holds work the server has not seen", () => {
    const confirmLeave = vi.fn(() => true);
    const ledger = draftLedger(confirmLeave);

    expect(ledger.mayLeave()).toBe(true);
    expect(confirmLeave).not.toHaveBeenCalled();

    ledger.hold("record posts id:one", true);
    expect(ledger.held()).toBe(true);
    expect(ledger.mayLeave()).toBe(true);
    expect(confirmLeave).toHaveBeenCalledTimes(1);

    // The owner releases the draft when the approved departure completes.
    // Until then, another attempted departure must still be guarded.
    expect(ledger.mayLeave()).toBe(true);
    expect(confirmLeave).toHaveBeenCalledTimes(2);

    ledger.hold("record posts id:one", false);
    expect(ledger.mayLeave()).toBe(true);
    expect(confirmLeave).toHaveBeenCalledTimes(2);
  });

  it("keeps the administrator on the resource when the draft is not to be lost", () => {
    const ledger = draftLedger(() => false);

    ledger.hold("collection-edit posts", true);
    expect(ledger.mayLeave()).toBe(false);
    expect(ledger.held()).toBe(true);

    ledger.hold("collection-edit posts", false);
    expect(ledger.mayLeave()).toBe(true);
  });

  it("lets the browser offer to keep a draft a reload would drop", () => {
    const ledger = draftLedger(() => true);
    render(<UnsavedDrafts ledger={ledger}><span /></UnsavedDrafts>);

    expect(unloading().defaultPrevented).toBe(false);
    ledger.hold("record posts id:one", true);
    expect(unloading().defaultPrevented).toBe(true);
  });

  it("holds one resource's draft and releases it on a save, a clean draft or an unmount", () => {
    const ledger = draftLedger(() => false);
    const wrapper = ({ children }: { children: ReactNode }) => <UnsavedDrafts ledger={ledger}>{children}</UnsavedDrafts>;
    const { result, rerender, unmount } = renderHook(
      ({ unsaved }: { unsaved: boolean }) => useUnsavedDraft("records posts", unsaved),
      { initialProps: { unsaved: true }, wrapper },
    );
    expect(ledger.held()).toBe(true);

    act(() => result.current());
    expect(ledger.held()).toBe(false);

    rerender({ unsaved: false });
    expect(ledger.held()).toBe(false);
    rerender({ unsaved: true });
    expect(ledger.held()).toBe(true);

    unmount();
    expect(ledger.held()).toBe(false);
  });
});
