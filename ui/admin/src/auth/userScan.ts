import { useCallback, useEffect, useRef, useState } from "react";
import { listUsers, type ManagedUser } from "../api/users";

// The users API has no search, filter or count: it pages by id (after, limit <= 100).
// A search therefore walks the pages in the background, keeps only the matches and can be
// stopped and resumed; the page never holds more than `matchCap` rows.

export type UserStatusFilter = "all" | "active" | "disabled" | "unverified";
export const userStatusFilters: UserStatusFilter[] = ["all", "active", "disabled", "unverified"];
const pageSize = 100;
const matchCap = 500;

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
/** A complete user id is opened directly instead of searched for. */
export const isUserId = (text: string) => uuid.test(text.trim());

/** Case-insensitive substring match on email or id, plus the status filter. */
export function userMatcher(query: string, status: UserStatusFilter): (user: ManagedUser) => boolean {
  const term = query.trim().toLowerCase();
  return (user) => {
    if (status === "active" && !user.active) return false;
    if (status === "disabled" && user.active) return false;
    if (status === "unverified" && user.verified) return false;
    return !term || user.email.toLowerCase().includes(term) || user.id.includes(term);
  };
}

export interface UserScan {
  matches: ManagedUser[];
  scanned: number;
  /** running; done (every user checked); paused (stopped, match cap or failure; resumable). */
  state: "running" | "done" | "paused";
  error: unknown;
  stop: () => void;
  resume: () => void;
}

/** Scans every user for `query`/`status` while `enabled`; restarts when either changes. */
export function useUserScan(query: string, status: UserStatusFilter, enabled: boolean): UserScan {
  const [matches, setMatches] = useState<ManagedUser[]>([]);
  const [scanned, setScanned] = useState(0);
  const [state, setState] = useState<UserScan["state"]>("done");
  const [error, setError] = useState<unknown>(null);
  const cursor = useRef<string | undefined>(undefined);
  const control = useRef<AbortController | null>(null);
  const found = useRef(0);

  const run = useCallback(
    async (match: (user: ManagedUser) => boolean) => {
      control.current?.abort();
      const abort = new AbortController();
      control.current = abort;
      setState("running");
      setError(null);
      try {
        for (;;) {
          const page = await listUsers({ after: cursor.current, limit: pageSize, signal: abort.signal });
          if (abort.signal.aborted) return;
          const hits = page.items.filter(match);
          found.current += hits.length;
          cursor.current = page.nextAfter;
          setScanned((count) => count + page.items.length);
          if (hits.length) setMatches((current) => [...current, ...hits]);
          if (!page.nextAfter) {
            setState("done");
            return;
          }
          if (found.current >= matchCap) {
            setState("paused");
            return;
          }
        }
      } catch (failure) {
        if (abort.signal.aborted) return;
        setError(failure);
        setState("paused");
      }
    },
    [],
  );

  useEffect(() => {
    cursor.current = undefined;
    found.current = 0;
    setMatches([]);
    setScanned(0);
    setError(null);
    if (!enabled) {
      setState("done");
      return;
    }
    void run(userMatcher(query, status));
    return () => control.current?.abort();
  }, [query, status, enabled, run]);

  const stop = useCallback(() => {
    control.current?.abort();
    setState("paused");
  }, []);
  const resume = useCallback(() => {
    found.current = 0;
    void run(userMatcher(query, status));
  }, [query, status, run]);

  return { matches, scanned, state, error, stop, resume };
}
