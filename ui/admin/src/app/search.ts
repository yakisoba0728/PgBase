import { useCallback, useEffect, useState } from "react";
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

/**
 * `value` once it has stopped changing for `delay` ms. A search keyed on it asks once per pause, and the query it
 * replaces is aborted (TanStack aborts a fetch whose `signal` was passed on once nothing observes it), so the latest wins.
 */
export function useDebounced<T>(value: T, delay = 250): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
