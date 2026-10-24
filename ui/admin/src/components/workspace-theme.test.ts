import { afterEach, describe, expect, it } from "vitest";
import { applyWorkspaceTheme, resetWorkspaceTheme } from "./workspace-theme";

afterEach(() => { delete document.documentElement.dataset.theme; delete document.documentElement.dataset.accent; document.documentElement.style.removeProperty("--accent"); document.documentElement.style.removeProperty("--action-text"); });
describe("workspace appearance", () => {
  it("returns signed-out appearance to system defaults after an explicit saved theme", () => {
    applyWorkspaceTheme({ theme: "light", accent: "#7460a2", branding: "Private workspace" });
    resetWorkspaceTheme();
    expect(document.documentElement.dataset.theme).toBe("system");
    expect(document.documentElement.dataset.accent).toBe("theme");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("var(--default-accent)");
    expect(document.documentElement.style.getPropertyValue("--action-text")).toBe("");
    expect(document.title).toBe("pgBase administration");
  });
  it("uses theme tokens for the legacy default so light and dark keep distinct palettes", () => {
    applyWorkspaceTheme({ theme: "light", accent: "#1D4ED8", branding: "Project" });
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("var(--default-accent)");
    expect(document.documentElement.dataset.theme).toBe("light");
    expect(document.title).toBe("Project");
    applyWorkspaceTheme({ theme: "dark", accent: "#1d4ed8", branding: "" });
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("var(--default-accent)");
  });
  it("uses the lavender theme default without making dark-mode button text dark", () => {
    applyWorkspaceTheme({ theme: "dark", accent: "#ded6ee", branding: "" });
    expect(document.documentElement.dataset.accent).toBe("theme");
    expect(document.documentElement.style.getPropertyValue("--action-text")).toBe("");
  });
  it("uses contrasting text for an explicit pale custom accent", () => {
    applyWorkspaceTheme({ theme: "light", accent: "#e7dcfa", branding: "" });
    expect(document.documentElement.style.getPropertyValue("--action-text")).toBe("#000");
  });
  it("uses the theme palette when a fresh server returns no saved accent", () => {
    applyWorkspaceTheme({ theme: "system", accent: "", branding: "" });
    expect(document.documentElement.dataset.accent).toBe("theme");
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe("var(--default-accent)");
    expect(document.documentElement.style.getPropertyValue("--action-text")).toBe("");
  });
  it("preserves custom saved colors across repeated application", () => {
    const application = { theme: "system" as const, accent: "#7460a2", branding: "Custom project" };
    applyWorkspaceTheme(application);
    applyWorkspaceTheme(application);
    expect(document.documentElement.style.getPropertyValue("--accent")).toBe(application.accent);
    expect(document.documentElement.dataset.accent).toBe("custom");
  });
});
