import { useEffect, useRef, useState } from "react";
import { useBlocker, type Location } from "react-router";
import { createStore } from "../app/store";
import { useT } from "../i18n";
import { ConfirmDialog } from "./Dialog";

interface UnsavedGuardProps {
  /** Unsaved edits exist. */
  dirty: boolean;
  /** A pending operation must settle before discarding its editor. */
  busy?: boolean;
  /** Read at navigation time: true lets this navigation through (a save or delete that leaves on purpose). */
  release?: () => boolean;
  /** Which navigations leave the edited thing; default any change of path or query. */
  leaves?: (current: Location, next: Location) => boolean;
  /** Runs when the administrator chooses to discard and leave. */
  onDiscard?: () => void;
  /** Runs before a reload or tab close (e.g. to persist a local draft). */
  onUnload?: () => void;
}

const anyChange = (current: Location, next: Location) => current.pathname + current.search !== next.pathname + next.search;

const guards = createStore(new Map<symbol, UnsavedGuardProps>());

/** All mounted editors share one Router blocker, including preserved schema drafts. */
export function UnsavedGuard(props: UnsavedGuardProps) {
  const [id] = useState(() => Symbol());
  const entries = guards.use();
  const current = useRef(props);
  current.current = props;
  useEffect(() => {
    const next = new Map(guards.get());
    next.set(id, { get dirty() { return current.current.dirty; }, get busy() { return current.current.busy; }, release: () => current.current.release?.() ?? false, leaves: (a, b) => (current.current.leaves ?? anyChange)(a, b), onDiscard: () => current.current.onDiscard?.() });
    guards.set(next);
    return () => { const next = new Map(guards.get()); next.delete(id); guards.set(next); };
  }, [id]);
  useEffect(() => { guards.set(new Map(guards.get())); }, [props.dirty, props.busy]);
  useEffect(() => {
    if (!props.dirty) return;
    const beforeUnload = (event: BeforeUnloadEvent) => { current.current.onUnload?.(); event.preventDefault(); };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [props.dirty]);
  return entries.keys().next().value === id ? <SharedBlocker /> : null;
}
function SharedBlocker() {
  const t = useT();
  const blocked = useRef<UnsavedGuardProps[]>([]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    blocked.current = [...guards.get().values()].filter((entry) => entry.dirty && !entry.release?.() && (entry.leaves ?? anyChange)(currentLocation, nextLocation));
    return blocked.current.length > 0;
  });
  return <ConfirmDialog busy={blocked.current.some((entry) => entry.busy)} open={blocker.state === "blocked"} title={t("unsaved.title")} message={t("unsaved.text")} confirmLabel={t("unsaved.discard")} danger
    onConfirm={() => { blocked.current.forEach((entry) => entry.onDiscard?.()); blocker.proceed?.(); }} onCancel={() => blocker.reset?.()} />;
}

/** A `leaves` test for editors that span sub-routes: anything outside `basePath` leaves. */
export const outside = (basePath: string) => (_current: Location, next: Location) => next.pathname !== basePath && !next.pathname.startsWith(`${basePath}/`);
