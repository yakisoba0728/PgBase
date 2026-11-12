import { useEffect, useRef } from "react";
import { useBlocker, type Location } from "react-router";
import { useT } from "../i18n";
import { ConfirmDialog } from "./Dialog";

interface UnsavedGuardProps {
  /** Unsaved edits exist. */
  dirty: boolean;
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

/**
 * The one unsaved-changes guard: in-app navigation asks first, a reload or tab close warns.
 * shortcut: react-router honours one blocker at a time, so only one editor per screen may mount a guard; upgrade to a guard registry if screens ever nest editors.
 */
export function UnsavedGuard({ dirty, release, leaves = anyChange, onDiscard, onUnload }: UnsavedGuardProps) {
  const t = useT();
  const unload = useRef(onUnload);
  unload.current = onUnload;
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && !release?.() && leaves(currentLocation, nextLocation));
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      unload.current?.();
      event.preventDefault();
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);
  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      title={t("unsaved.title")}
      message={t("unsaved.text")}
      confirmLabel={t("unsaved.discard")}
      danger
      onConfirm={() => {
        onDiscard?.();
        blocker.proceed?.();
      }}
      onCancel={() => blocker.reset?.()}
    />
  );
}

/** A `leaves` test for editors that span sub-routes: anything outside `basePath` leaves. */
export const outside = (basePath: string) => (_current: Location, next: Location) => next.pathname !== basePath && !next.pathname.startsWith(`${basePath}/`);
