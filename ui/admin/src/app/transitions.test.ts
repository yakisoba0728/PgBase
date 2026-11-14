import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { navigationTransition } from "./transitions";

describe("navigationTransition", () => {
  beforeEach(() => {
    vi.stubGlobal("matchMedia", () => ({ matches: false }));
    Object.defineProperty(document, "startViewTransition", { value: () => {}, configurable: true });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    delete (document as Partial<Document>).startViewTransition;
    document.body.innerHTML = "";
  });

  it("plays none for a search-only change or the same path with a query", () => {
    expect(navigationTransition("/admin/collections/posts", "", "/admin")).toBeNull();
    expect(navigationTransition("/admin/collections/posts", "?record=1", "/admin")).toBeNull();
    expect(navigationTransition("/admin/collections/posts", "/collections/posts?record=1", "/admin")).toBeNull();
  });

  it("cross-fades the content for another screen", () => {
    expect(navigationTransition("/admin/logs", "/jobs", "/admin")).toBe("route");
  });

  it("leaves sibling tabs to the page body", () => {
    document.body.innerHTML = '<nav class="page-tabs"><a href="/admin/settings">a</a><a href="/admin/settings/status">b</a></nav>';
    expect(navigationTransition("/admin/settings", "/settings/status", "/admin")).toBeNull();
  });

  it("fades the whole page into or out of a guest screen", () => {
    expect(navigationTransition("/admin/login", "/", "/admin")).toBe("guest");
    expect(navigationTransition("/admin/logs", "/login", "/admin")).toBe("guest");
  });

  it("plays none under reduced motion", () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true }));
    expect(navigationTransition("/admin/logs", "/jobs", "/admin")).toBeNull();
  });
});
