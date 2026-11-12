import { afterEach, describe, expect, it, vi } from "vitest";
import { comboOf, dispatch, keyLabel, registerShortcut } from "./keys";

const press = (key: string, init: KeyboardEventInit = {}, target?: HTMLElement) => {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  if (target) Object.defineProperty(event, "target", { value: target });
  return event;
};

const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((cleanup) => cleanup());
  document.body.innerHTML = "";
});

describe("shortcut registry", () => {
  it("normalises combos", () => {
    expect(comboOf(press("K", { metaKey: true }))).toBe("mod+k");
    expect(comboOf(press("k", { ctrlKey: true }))).toBe("mod+k");
    expect(comboOf(press("?", { shiftKey: true }))).toBe("?");
    expect(comboOf(press("Escape"))).toBe("Escape");
    expect(keyLabel("mod+k", true)).toEqual(["⌘K"]);
    expect(keyLabel("g l", false)).toEqual(["G", "L"]);
  });

  it("runs the latest registration, and passes on when it declines", () => {
    const first = vi.fn();
    const second = vi.fn(() => false as const);
    cleanups.push(registerShortcut({ keys: "n", run: first }), registerShortcut({ keys: "n", run: second }));
    expect(dispatch(press("n"))).toBe(true);
    expect(second).toHaveBeenCalledOnce();
    expect(first).toHaveBeenCalledOnce();
  });

  it("ignores plain keys while typing or composing, but not combos marked for fields", () => {
    const plain = vi.fn();
    const combo = vi.fn();
    cleanups.push(registerShortcut({ keys: "/", run: plain }), registerShortcut({ keys: "mod+k", run: combo, inFields: true }));
    const input = document.createElement("input");
    document.body.append(input);
    expect(dispatch(press("/", {}, input))).toBe(false);
    expect(dispatch(press("k", { metaKey: true }, input))).toBe(true);
    expect(dispatch(press("/", { isComposing: true }))).toBe(false);
    expect(plain).not.toHaveBeenCalled();
    expect(combo).toHaveBeenCalledOnce();
  });

  it("stays quiet behind an open dialog unless allowed", () => {
    const run = vi.fn();
    cleanups.push(registerShortcut({ keys: "?", run }));
    const dialog = document.createElement("dialog");
    dialog.setAttribute("open", "");
    document.body.append(dialog);
    expect(dispatch(press("?"))).toBe(false);
    expect(run).not.toHaveBeenCalled();
  });

  it("follows two-key sequences within a short window", () => {
    const run = vi.fn();
    cleanups.push(registerShortcut({ keys: "g l", run }));
    expect(dispatch(press("g"), 0)).toBe(true);
    expect(dispatch(press("l"), 500)).toBe(true);
    expect(run).toHaveBeenCalledOnce();
    dispatch(press("g"), 1000);
    expect(dispatch(press("l"), 5000)).toBe(false);
    expect(run).toHaveBeenCalledOnce();
  });
});
