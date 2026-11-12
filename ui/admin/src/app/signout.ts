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

/** The one sign-out: ends the server session (best effort), then forgets device state. The caller navigates to /login. */
export async function signOut(): Promise<void> {
  try {
    await logout();
  } finally {
    forgetSession();
  }
}

/** Sign-out as an action for menus and the command palette. */
export function useSignOut(): () => void {
  const t = useT();
  const navigate = useNavigate();
  const mutation = useMutation({
    mutationFn: signOut,
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => void navigate("/login", { replace: true }),
  });
  return () => mutation.mutate();
}
