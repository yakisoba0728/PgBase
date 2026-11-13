import { act, renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { useDraft } from "./useDraft";
it("preserves dirty policy/token inputs across equivalent or changed background data", () => {
  const { result, rerender } = renderHook(({ value }) => useDraft({ value }), { initialProps: { value: "before" } });
  act(() => result.current.patch({ value: "mine" }));
  rerender({ value: "before" });
  expect(result.current.draft.value).toBe("mine");
  rerender({ value: "remote" });
  expect(result.current.draft.value).toBe("mine");
  act(() => result.current.reset());
  expect(result.current.draft.value).toBe("remote");
});
