import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Collection, CollectionField } from "../api/collections";
import { RecordCell } from "./RecordCell";
import { RelationPicker } from "./RelationPicker";
import { htmlText } from "./relation";

const body: CollectionField = {
  id: "body", name: "body", columnName: "body", type: "editor", required: false,
  unique: false, indexed: false, ordinal: 0, hidden: false, presentable: true,
};
const target: Collection = { id: "target", name: "notes", tableName: "notes", fields: [body], createdAt: "" };
const relation: CollectionField = {
  ...body, id: "related", name: "related", type: "relation",
  relation: { targetCollectionId: target.id, cardinality: "multiple", minSelect: 0, maxSelect: 10, onDelete: "restrict", valueCodecVersion: 1, storage: { kind: "edge_table_v1", name: "related_edges" } },
};
const payloads = [
  "&lt;img src=x onerror=alert(1)&gt;",
  "&lt;script&gt;alert(1)&lt;/script&gt;",
  "<scr<script></script>ipt>alert(1)</script>",
  '<svg><a href="javascript:alert(1)">untrusted</a></svg>',
];

describe("HTML summaries are text at every rendering boundary", () => {
  it.each(payloads)("renders editor cells and relation chips safely: %s", (payload) => {
    const { container } = render(<table><tbody><tr>
      <RecordCell field={body} name="body" record={{ id: "r1", body: payload }} collection="notes" catalog={[target]} settled={false} onPeek={vi.fn()} />
      <RecordCell field={relation} name="related" record={{ id: "r2", related: ["r1"], expand: { related: [{ id: "r1", body: payload }] } }} collection="notes" catalog={[target]} settled={false} onPeek={vi.fn()} />
    </tr></tbody></table>);
    expect(container.querySelector(".cell-text")?.textContent).toBe(htmlText(payload, 300));
    expect(container.querySelector(".chip")?.textContent).toBe(htmlText(payload));
    expect(container.querySelector("script, img, svg:not([aria-hidden]), a[href^='javascript:'], [onerror]")).toBeNull();
  });

  it("renders selected relation labels as text, including decoded markup", () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    client.setQueryData(["collections"], { collections: { target } });
    const payload = payloads[0];
    const { container, unmount } = render(<QueryClientProvider client={client}><MemoryRouter>
      <RelationPicker field={relation} value={["r1"]} known={[{ id: "r1", body: payload }]} onChange={vi.fn()} readOnly />
    </MemoryRouter></QueryClientProvider>);
    expect(container.textContent).toContain(htmlText(payload));
    expect(container.querySelector("script, img, [onerror]")).toBeNull();
    unmount();
    client.clear();
  });
});
