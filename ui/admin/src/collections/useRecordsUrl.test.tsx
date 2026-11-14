import type { ReactNode } from "react";
import { act, renderHook } from "@testing-library/react";
import { BrowserRouter, useLocation } from "react-router";
import { describe, expect, it } from "vitest";
import type { CollectionField } from "../api/collections";
import { leavesRecord } from "./useRecordForm";
import { buildFilter, nextSort, useRecordsUrl } from "./useRecordsUrl";

describe("nextSort", () => {
  it("cycles ascending, descending, unsorted", () => {
    expect(nextSort("", "title")).toBe("title");
    expect(nextSort("title", "title")).toBe("-title");
    expect(nextSort("-title", "title")).toBe("");
    expect(nextSort("-created_at", "title")).toBe("title");
  });
});

describe("buildFilter", () => {
  const fields = [{ name: "title", type: "text" }, { name: "body", type: "editor" }, { name: "n", type: "integer" }] as CollectionField[];
  it("searches text fields for plain words and passes expressions through", () => {
    expect(buildFilter("  Draft ", fields)).toBe("title:lower ~ 'draft' || body:lower ~ 'draft'");
    const wide = Array.from({ length: 40 }, (_, index) => ({ name: `f${index}`, type: "text", presentable: index === 39 }) as CollectionField);
    const clauses = buildFilter("x", wide).split(" || ");
    expect(clauses).toHaveLength(30);
    expect(clauses[0]).toBe("f39:lower ~ 'x'");
    expect(buildFilter("n > 3", fields)).toBe("n > 3");
    expect(buildFilter("", fields)).toBe("");
  });
  it("searches a word with an apostrophe, escaping the quote", () => {
    expect(buildFilter("O'Brien", fields)).toBe("title:lower ~ 'o''brien' || body:lower ~ 'o''brien'");
    expect(buildFilter("O'Brien", [])).toBe("id = 'O''Brien'");
  });
});

describe("useRecordsUrl", () => {
  const setup = (entry: string) => {
    // Updates read the live URL (window.location), so the tests run on the browser history.
    window.history.replaceState(null, "", entry);
    const wrapper = ({ children }: { children: ReactNode }) => <BrowserRouter>{children}</BrowserRouter>;
    return renderHook(() => ({ url: useRecordsUrl(), location: useLocation() }), { wrapper });
  };

  it("reads and writes filter, sort and the open record in the query string", () => {
    const { result } = setup("/collections/posts?filter=a%20%3D%201&sort=-title");
    expect(result.current.url.filter).toBe("a = 1");
    expect(result.current.url.sort).toBe("-title");
    act(() => result.current.url.toggleSort("title"));
    expect(new URLSearchParams(result.current.location.search).has("sort")).toBe(false);
    act(() => result.current.url.open("r1"));
    act(() => result.current.url.setFilter("published = true"));
    const params = new URLSearchParams(result.current.location.search);
    expect(params.get("record")).toBe("r1");
    expect(params.get("filter")).toBe("published = true");
    expect(result.current.location.pathname).toBe("/collections/posts");
  });

  it("keeps both of two changes made before a re-render", () => {
    const { result } = setup("/collections/posts?filter=old");
    act(() => {
      result.current.url.setFilter("first");
      result.current.url.open("r1");
    });
    const params = new URLSearchParams(result.current.location.search);
    expect(params.get("filter")).toBe("first");
    expect(params.get("record")).toBe("r1");
  });

  it("clears a rejected sort together with the filter", () => {
    const { result } = setup("/collections/posts?filter=a&sort=-nope&record=r1");
    act(() => result.current.url.clearQuery());
    expect(new URLSearchParams(result.current.location.search).toString()).toBe("record=r1");
  });

  it("keeps creating separate from a record id and clears both on close", () => {
    const { result } = setup("/collections/posts?record=new");
    expect(result.current.url.record).toBe("new");
    expect(result.current.url.creating).toBe(false);
    act(() => result.current.url.create());
    expect(result.current.url.creating).toBe(true);
    expect(result.current.url.record).toBeNull();
    act(() => result.current.url.close());
    expect(result.current.location.search).toBe("");
  });
});

describe("leavesRecord", () => {
  it("ignores filter and sort changes but not a different record", () => {
    const at = (search: string) => ({ pathname: "/collections/posts", search });
    expect(leavesRecord(at("?record=1"), at("?record=1&sort=title"))).toBe(false);
    expect(leavesRecord(at("?record=1"), at("?record=2"))).toBe(true);
    expect(leavesRecord(at("?new=1"), at(""))).toBe(true);
    expect(leavesRecord(at("?record=1"), { pathname: "/logs", search: "?record=1" })).toBe(true);
  });
});
