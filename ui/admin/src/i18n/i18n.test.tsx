import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { en } from "./en";
import { ko } from "./ko";

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe("i18n", () => {
  it("has a non-empty Korean string for every English key", () => {
    expect(Object.keys(ko).sort()).toEqual(Object.keys(en).sort());
    expect(Object.values(ko).every((text) => text.trim() !== "")).toBe(true);
  });

  it("interpolates variables and leaves unknown placeholders alone", async () => {
    const { translate } = await import("./index");
    expect(translate("en", "setup.restoring", { phase: "copy" })).toBe("Restoring: copy");
    expect(translate("ko", "setup.restoring", { phase: "copy" })).toBe("복원 중: copy");
    expect(translate("en", "setup.restoring")).toBe("Restoring: {phase}");
  });

  it("defaults to the browser language", async () => {
    vi.stubGlobal("navigator", { ...navigator, language: "ko-KR" });
    const { useT } = await import("./index");
    expect(renderHook(() => useT()).result.current("login.title")).toBe("로그인");
  });

  it("switches, persists and re-renders on setLanguage", async () => {
    vi.stubGlobal("navigator", { ...navigator, language: "en-US" });
    const { setLanguage, useT } = await import("./index");
    const { result } = renderHook(() => useT());
    expect(result.current("login.title")).toBe("Sign in");
    act(() => setLanguage("ko"));
    expect(result.current("login.title")).toBe("로그인");
    expect(localStorage.getItem("pgbase.language")).toBe("ko");
    expect(document.documentElement.lang).toBe("ko");
  });

  it("prefers the stored language over the browser one", async () => {
    localStorage.setItem("pgbase.language", "en");
    vi.stubGlobal("navigator", { ...navigator, language: "ko-KR" });
    const { useLanguage } = await import("./index");
    expect(renderHook(() => useLanguage()).result.current).toBe("en");
  });
});
