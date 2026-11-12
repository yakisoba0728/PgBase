import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { logout } from "../api/session";
import { clearAllDrafts } from "../collections/recordDraft";
import { drafts } from "../schema/drafts";
import { errorText, queryClient } from "./queries";
import { forgetRecent } from "./recent";

/**
 * Content this device kept for the signed-in administrator: record drafts, schema drafts, recent collections
 * and this tab's SQL text and pending import. Preferences (theme, language, layout, columns) stay.
 */
function forgetDevice(): void {
  clearAllDrafts();
  drafts.set({});
  forgetRecent();
  try {
    for (const name of Object.keys(sessionStorage)) if (name.startsWith("pgbase.")) sessionStorage.removeItem(name);
  } catch {
    // Nothing stored.
  }
}

/** Cached server data; dropped by /login once the shell has unmounted, so nothing refetches with the ended session. */
export const forgetSession = () => queryClient.clear();

/** Ends the session on this device: forgets its content now, then leaves for /login (which drops the cache). */
export function useLeaveSession(): () => void {
  const navigate = useNavigate();
  return () => {
    forgetDevice();
    void navigate("/login", { replace: true, state: { signedOut: true } });
  };
}

/** The one sign-out, for menus and the command palette: ends the server session (best effort), then leaves. */
export function useSignOut(): () => void {
  const t = useT();
  const leave = useLeaveSession();
  const mutation = useMutation({
    mutationFn: logout,
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: leave,
  });
  return () => mutation.mutate();
}
