import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { logout } from "../api/session";
import { clearAllDrafts } from "../collections/recordDraft";
import { drafts } from "../schema/drafts";
import { errorText, queryClient } from "./queries";
import { forgetRecent } from "./recent";

/** Forgets what this device kept for the signed-in administrator: cached data, record and schema drafts, recent collections. Theme, language and layout stay. */
export function forgetSession(): void {
  queryClient.clear();
  clearAllDrafts();
  drafts.set({});
  forgetRecent();
  try {
    sessionStorage.removeItem("pgbase.operations.discovered");
  } catch {
    // Nothing stored.
  }
}

/** Leaves for /login, which forgets device state once the shell has unmounted (so nothing refetches with the ended session). */
export function useLeaveSession(): () => void {
  const navigate = useNavigate();
  return () => void navigate("/login", { replace: true, state: { signedOut: true } });
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
