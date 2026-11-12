import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  vi.resetModules();
});

describe("theme store", () => {
  it("defaults to auto, leaving data-theme unset", async () => {
    const { useTheme } = await import("./theme");
    expect(useTheme).toBeTypeOf("function");
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it("applies the stored mode on load", async () => {
    localStorage.setItem("pgbase.theme", "dark");
    await import("./theme");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("persists and applies changes, and auto clears the attribute", async () => {
    const { setTheme } = await import("./theme");
    setTheme("light");
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(localStorage.getItem("pgbase.theme")).toBe("light");
    setTheme("auto");
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("pgbase.theme")).toBe("auto");
  });

  it("ignores an unknown stored value", async () => {
    localStorage.setItem("pgbase.theme", "purple");
    await import("./theme");
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });
});
