import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { en } from "./en";
import { ko } from "./ko";

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.doUnmock("./ko");
  vi.resetModules();
});

describe("i18n", () => {
  it("has a non-empty Korean string for every English key", () => {
    expect(Object.keys(ko).sort()).toEqual(Object.keys(en).sort());
    expect(Object.values(ko).every((text) => text.trim() !== "")).toBe(true);
  });

  it("interpolates variables and leaves unknown placeholders alone", async () => {
    const { loadLanguage, translate } = await import("./index");
    await Promise.all([loadLanguage("en"), loadLanguage("ko")]);
    expect(translate("en", "setup.restoring", { phase: "copy" })).toBe("Restoring: copy");
    expect(translate("ko", "setup.restoring", { phase: "copy" })).toBe("복원 중: copy");
    expect(translate("en", "setup.restoring")).toBe("Restoring: {phase}");
    expect(translate("en", "records.count", { count: 1 })).toBe("1 record");
    expect(translate("en", "records.count", { count: 2 })).toBe("2 records");
  });

  it("defaults to the browser language", async () => {
    vi.stubGlobal("navigator", { ...navigator, language: "ko-KR" });
    const { loadInitialLanguage, useT } = await import("./index");
    await loadInitialLanguage();
    expect(renderHook(() => useT()).result.current("login.title")).toBe("로그인");
  });

  it("switches, persists and re-renders on setLanguage", async () => {
    vi.stubGlobal("navigator", { ...navigator, language: "en-US" });
    const { loadInitialLanguage, setLanguage, useT } = await import("./index");
    await loadInitialLanguage();
    const { result } = renderHook(() => useT());
    expect(result.current("login.title")).toBe("Sign in");
    // Korean has not been fetched yet: the switch lands once its dictionary arrives.
    await act(() => setLanguage("ko"));
    expect(result.current("login.title")).toBe("로그인");
    expect(localStorage.getItem("pgbase.language")).toBe("ko");
    expect(document.documentElement.lang).toBe("ko");
    // A loaded language switches synchronously.
    act(() => void setLanguage("en"));
    expect(result.current("login.title")).toBe("Sign in");
  });

  it("prefers the stored language over the browser one", async () => {
    localStorage.setItem("pgbase.language", "en");
    vi.stubGlobal("navigator", { ...navigator, language: "ko-KR" });
    const { useLanguage } = await import("./index");
    expect(renderHook(() => useLanguage()).result.current).toBe("en");
  });

  it("answers whether a computed key has a message", async () => {
    const { hasMessage, loadInitialLanguage } = await import("./index");
    await loadInitialLanguage();
    expect(hasMessage("error.generic")).toBe(true);
    expect(hasMessage("error.no_such_code")).toBe(false);
  });

  it("falls back to English for this load when the stored language cannot be fetched", async () => {
    localStorage.setItem("pgbase.language", "ko");
    vi.doMock("./ko", () => Promise.reject(new Error("chunk failed")));
    const { loadInitialLanguage, useLanguage, useT } = await import("./index");
    await loadInitialLanguage();
    expect(renderHook(() => useLanguage()).result.current).toBe("en");
    expect(renderHook(() => useT()).result.current("login.title")).toBe("Sign in");
    expect(document.documentElement.lang).toBe("en");
    // The stored choice is kept for a later load.
    expect(localStorage.getItem("pgbase.language")).toBe("ko");
  });

  it("keeps the current language when a switch cannot fetch its dictionary", async () => {
    localStorage.setItem("pgbase.language", "en");
    vi.doMock("./ko", () => Promise.reject(new Error("chunk failed")));
    const { loadInitialLanguage, setLanguage, useLanguage } = await import("./index");
    await loadInitialLanguage();
    const { result } = renderHook(() => useLanguage());
    await act(() => setLanguage("ko"));
    expect(result.current).toBe("en");
    expect(localStorage.getItem("pgbase.language")).toBe("en");
  });
});
