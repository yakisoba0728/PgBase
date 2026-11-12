// Command palette matching: pure functions, tested on their own.

export interface PaletteItem {
  id: string;
  group: "recent" | "collections" | "screens" | "actions";
  label: string;
  /** Extra words that should find the item (a path, the other language's name). */
  keywords?: string;
  run: () => void;
}

/**
 * How well `query` matches `text` (higher is better, -1 for no match): a prefix beats a word start,
 * which beats a substring, which beats letters in order with gaps.
 */
export function matchScore(query: string, text: string): number {
  const needle = query.trim().toLowerCase();
  if (!needle) return 0;
  const hay = text.toLowerCase();
  const at = hay.indexOf(needle);
  if (at === 0) return 1000 - hay.length;
  if (at > 0) return (/[\s/_.-]/.test(hay[at - 1]) ? 800 : 600) - at - hay.length / 100;
  // Letters in order: fewer and shorter gaps score higher.
  let position = -1;
  let gaps = 0;
  for (const letter of needle) {
    if (letter === " ") continue;
    const next = hay.indexOf(letter, position + 1);
    if (next < 0) return -1;
    if (position >= 0 && next > position + 1) gaps += next - position;
    position = next;
  }
  return Math.max(1, 400 - gaps * 10 - hay.length / 100);
}

const groupOrder: PaletteItem["group"][] = ["recent", "collections", "screens", "actions"];

/** Items for a query: all of them in group order when empty, else the matches best first (at most `limit`). */
export function rankItems(items: PaletteItem[], query: string, limit = 50): PaletteItem[] {
  if (!query.trim()) return [...items].sort((a, b) => groupOrder.indexOf(a.group) - groupOrder.indexOf(b.group)).slice(0, limit);
  return items
    .filter((item) => item.group !== "recent")
    .map((item) => ({ item, score: Math.max(matchScore(query, item.label), item.keywords ? matchScore(query, item.keywords) - 50 : -1) }))
    .filter((entry) => entry.score >= 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, limit)
    .map((entry) => entry.item);
}
