import { useCallback } from "react";
import { useSearchParams, type NavigateOptions } from "react-router";

/** Query-string state for a screen; empty values are removed. */
export function useSearchState() {
  const [params, setParams] = useSearchParams();
  const update = useCallback(
    (changes: Record<string, string | null | undefined>, options?: NavigateOptions) => {
      // The live URL, not the render's params: a quick second change would otherwise undo the first.
      const next = new URLSearchParams(window.location.search);
      for (const [key, value] of Object.entries(changes)) {
        if (value) next.set(key, value);
        else next.delete(key);
      }
      setParams(next, options);
    },
    [setParams],
  );
  return { params, update };
}
