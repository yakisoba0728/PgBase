import { useNavigate } from "react-router";
import { toast } from "../components/Toast";
import { tNow } from "../i18n";
import { ApiError } from "../api/http";
import { getSession, logout, type AdminSession } from "../api/session";
import { clearAllDrafts } from "../collections/recordDraft";
import { drafts } from "../schema/drafts";
import { forgetOperations } from "./operations";
import { errorText, isUnauthorized, queryClient, sessionQuery, signingOutKey } from "./queries";
import { forgetRecent } from "./recent";
import { readStorage, removeStorage, writeStorage } from "./store";

/** This tab's own content: schema drafts in memory and its SQL text and pending import. */
function forgetTab(): void {
  drafts.set({});
  try {
    for (const name of Object.keys(sessionStorage)) if (name.startsWith("pgbase.")) sessionStorage.removeItem(name);
  } catch {
    // Nothing stored.
  }
}

/**
 * Content this device kept for the signed-in administrator: the tab's content plus record drafts, recent collections
 * and tracked operations. Preferences (theme, language, layout, columns) stay.
 */
function forgetDevice(): void {
  forgetTab();
  clearAllDrafts();
  forgetRecent();
  forgetOperations();
}

// The administrator this device's content belongs to; another one signing in starts from nothing.
const ownerKey = "pgbase.owner";

/** A session from sign-in or setup becomes this tab's: no cached data of an earlier session survives it. */
export function adoptSession(session: AdminSession): void {
  const owner = readStorage(ownerKey);
  if (owner !== null && owner !== session.administrator.id) forgetDevice();
  writeStorage(ownerKey, session.administrator.id);
  removeStorage(signingOutKey);
  queryClient.clear();
  queryClient.setQueryData(sessionQuery.queryKey, session);
}

/**
 * Renews this tab's session after a CSRF rejection or another tab's sign-in and returns who is signed in now: the
 * same administrator keeps working; a different one replaces everything this tab showed (the shell remounts on the
 * new id). Whether a rejected request is resent is decided against the administrator it was issued under (http.ts).
 */
export async function recoverSession(): Promise<string | null> {
  const cached = queryClient.getQueryData(sessionQuery.queryKey);
  if (!cached) return null;
  let session: AdminSession;
  try {
    session = await getSession();
  } catch (error) {
    if (isUnauthorized(error)) void queryClient.resetQueries({ queryKey: sessionQuery.queryKey });
    return null;
  }
  const id = session.administrator.id;
  if (id !== cached.administrator.id) {
    forgetTab();
    queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== sessionQuery.queryKey[0] });
    // The screen that asked has been remounted, so the reason is told here.
    toast(tNow("error.session_changed"), "danger");
  }
  queryClient.setQueryData(sessionQuery.queryKey, session);
  return id;
}

/** The administrator this tab's requests are issued under. */
export const currentAdministrator = () => queryClient.getQueryData(sessionQuery.queryKey)?.administrator.id ?? null;

if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === ownerKey) void recoverSession();
  });
}

/** Leaves for /login; the committed navigation then signs out (see followSignOut), so a blocked navigation keeps everything. */
export function useLeaveSession(end = false): () => void {
  const navigate = useNavigate();
  return () => void navigate("/login", { replace: true, state: { signedOut: true, end } });
}

/** The one sign-out, for menus and the command palette: ends the server session once leaving is confirmed. */
export const useSignOut = () => useLeaveSession(true);

let followed: string | null = null;

/**
 * Router subscriber: a sign-out takes effect the moment its navigation commits (an unsaved-changes Cancel never
 * commits it), before /login renders, so a reload or a typed address right after cannot skip it.
 */
export function followSignOut(location: { key: string; state: unknown }): void {
  const leaving = location.state as { signedOut?: boolean; end?: boolean } | null;
  if (!leaving?.signedOut || followed === location.key) return;
  followed = location.key;
  finishSignOut(leaving.end === true);
}

/**
 * Forgets this device's content and cache at once (the guard then has no session to show), then ends the server
 * session. The request outlives the page, and until the server confirms it the next session check ends it first.
 */
export function finishSignOut(end: boolean): void {
  forgetDevice();
  queryClient.clear();
  if (!end) return;
  writeStorage(signingOutKey, "1");
  // The cache is gone, so a stale token (another tab signed in again) is renewed here rather than by recoverSession.
  const renewed = (error: unknown) => (error instanceof ApiError && error.code === "csrf_rejected" ? getSession().then(() => logout(true)) : Promise.reject(error));
  logout(true)
    .catch(renewed)
    .then(
      () => removeStorage(signingOutKey),
      (error: unknown) => {
        if (isUnauthorized(error)) removeStorage(signingOutKey);
        else toast(errorText(tNow, error), "danger");
      },
    );
}
