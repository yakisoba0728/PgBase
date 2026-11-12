import { useNavigate } from "react-router";
import { toast } from "../components/Toast";
import { tNow, type Translate } from "../i18n";
import { ApiError, type CsrfRecovery } from "../api/http";
import { getSession, logout, type AdminSession } from "../api/session";
import { clearAllDrafts } from "../collections/recordDraft";
import { drafts } from "../schema/drafts";
import { forgetOperations } from "./operations";
import { errorText, isUnauthorized, queryClient, sessionQuery } from "./queries";
import { forgetRecent } from "./recent";
import { readStorage, writeStorage } from "./store";

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
  queryClient.clear();
  queryClient.setQueryData(sessionQuery.queryKey, session);
}

/**
 * Renews this tab's session after a CSRF rejection or another tab's sign-in: the same administrator keeps working
 * (the request is retried); a different one replaces everything this tab showed (the shell remounts on the new id).
 */
export async function recoverSession(): Promise<CsrfRecovery> {
  const cached = queryClient.getQueryData(sessionQuery.queryKey);
  if (!cached) return null;
  let session: AdminSession;
  try {
    session = await getSession();
  } catch (error) {
    if (isUnauthorized(error)) void queryClient.resetQueries({ queryKey: sessionQuery.queryKey });
    return null;
  }
  if (session.administrator.id === cached.administrator.id) {
    queryClient.setQueryData(sessionQuery.queryKey, session);
    return "retry";
  }
  forgetTab();
  queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== sessionQuery.queryKey[0] });
  queryClient.setQueryData(sessionQuery.queryKey, session);
  // The screen that asked has been remounted, so the reason is told here.
  toast(tNow("error.session_changed"), "danger");
  return "changed";
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === ownerKey) void recoverSession();
  });
}

/** Leaves for /login; LoginScreen then forgets this device's content (see finishSignOut), so a blocked navigation keeps everything. */
export function useLeaveSession(end = false): () => void {
  const navigate = useNavigate();
  return () => void navigate("/login", { replace: true, state: { signedOut: true, end } });
}

/** The one sign-out, for menus and the command palette: ends the server session once /login is reached. */
export const useSignOut = () => useLeaveSession(true);

/** Runs on /login after a sign-out committed: forgets this device's content and cache, then ends the server session (best effort). */
export function finishSignOut(end: boolean, t: Translate): void {
  forgetDevice();
  queryClient.clear();
  if (!end) return;
  // The cache is gone, so a stale token (another tab signed in again) is renewed here rather than by recoverSession.
  const renewed = (error: unknown) => (error instanceof ApiError && error.code === "csrf_rejected" ? getSession().then(logout) : Promise.reject(error));
  logout()
    .catch(renewed)
    .catch((error: unknown) => {
      if (!isUnauthorized(error)) toast(errorText(t, error), "danger");
    });
}
