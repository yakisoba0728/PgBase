import { describe, expect, it } from "vitest";
import { matchScore, rankItems, type PaletteItem } from "./palette";

const item = (label: string, group: PaletteItem["group"] = "screens", keywords?: string): PaletteItem => ({ id: label, label, group, keywords, run: () => {} });

describe("palette matching", () => {
  it("ranks prefix over word start over substring over scattered letters", () => {
    const prefix = matchScore("log", "Logs");
    const word = matchScore("log", "Request logs");
    const inner = matchScore("log", "Catalog");
    const scattered = matchScore("lgs", "Logs");
    expect(prefix).toBeGreaterThan(word);
    expect(word).toBeGreaterThan(inner);
    expect(inner).toBeGreaterThan(scattered);
    expect(scattered).toBeGreaterThan(0);
    expect(matchScore("xyz", "Logs")).toBe(-1);
    expect(matchScore("", "Logs")).toBe(0);
  });

  it("is case-insensitive and finds by keywords", () => {
    const items = [item("Settings"), item("SQL console", "screens", "/sql query"), item("products", "collections")];
    expect(rankItems(items, "QUERY").map((entry) => entry.label)).toEqual(["SQL console"]);
    expect(rankItems(items, "prod")[0].label).toBe("products");
  });

  it("lists recent first when empty and hides the recent duplicates while searching", () => {
    const items = [item("Logs"), item("products", "collections"), item("products", "recent"), item("Sign out", "actions")];
    expect(rankItems(items, "").map((entry) => entry.group)).toEqual(["recent", "collections", "screens", "actions"]);
    expect(rankItems(items, "products").map((entry) => entry.group)).toEqual(["collections"]);
  });
});
