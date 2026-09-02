import { useState } from "react";
import { SessionProvider, useSession } from "./auth/session";
import { AdminLink } from "./routing/admin-link";
import { useAdminLocation, type AdminHistory, type AdminRoute } from "./routing/admin-location";
import { UnsavedDrafts, draftLedger } from "./routing/unsaved-draft";
import type { AdminBackend } from "./backend/ports";
import { CollectionDesignerPage } from "./pages/collection-designer-page";
import { CollectionsPage } from "./pages/collections-page";
import { RecordEditPage } from "./pages/record-edit-page";
import { RecordsPage } from "./pages/records-page";
import { SystemPage } from "./pages/system-page";
import { LoginPage } from "./pages/login-page";
import { SetupPage } from "./pages/setup-page";

type Navigate = (route: AdminRoute) => void;

// The route identity keys the outlet, so leaving a resource drops its page
// state before the next one renders instead of each page unwinding it.
function outlet(route: AdminRoute, key: string, navigate: Navigate) {
  if (route.kind === "collection-create") return <CollectionDesignerPage key={key} mode={{ kind: "create" }} navigate={navigate} />;
  if (route.kind === "system") return <SystemPage key={key} />;
  if (route.kind === "collection-edit") return <CollectionDesignerPage key={key} mode={{ kind: "edit", collection: route.collection }} navigate={navigate} />;
  if (route.kind === "records") return <RecordsPage collectionName={route.collection} key={key} navigate={navigate} />;
  if (route.kind === "record") return <RecordEditPage collectionName={route.collection} id={route.id} key={key} navigate={navigate} />;
  return <CollectionsPage key={key} navigate={navigate} />;
}

function AuthenticatedShell({ history }: { history?: AdminHistory }) {
  const { logout, state } = useSession();
  const [drafts] = useState(draftLedger);
  const { identity, navigate, route } = useAdminLocation(history, drafts.mayLeave);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  if (state.kind !== "authenticated") return null;
  async function signOut() {
    // Signing out ends the session the draft would have been saved under, so it
    // is a departure the administrator is asked about like any other.
    if (logoutPending || !drafts.mayLeave()) return;
    setLogoutPending(true);
    setLogoutError(null);
    try {
      await logout();
    } catch {
      setLogoutError("Unable to sign out. Try again.");
      setLogoutPending(false);
    }
  }
  return <main className="shell">
    <header>
      <div>
        <h1>pgBase administration</h1>
        <p>{state.administrator.email}</p>
      </div>
      <button disabled={logoutPending} onClick={() => void signOut()} type="button">{logoutPending ? "Signing out…" : "Sign out"}</button>
    </header>
    {logoutError && <p className="form-error" role="alert">{logoutError}</p>}
    <nav aria-label="Administration"><AdminLink className="text-button" navigate={navigate} route={{ kind: "collections" }}>Collections</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "system" }}>System</AdminLink></nav>
    <UnsavedDrafts ledger={drafts}>{outlet(route, identity, navigate)}</UnsavedDrafts>
  </main>;
}

function SessionScreen({ history }: { history?: AdminHistory }) {
  const { login, sessionError, setup, state } = useSession();
  if (state.kind === "loading") return <main className="auth-page"><p aria-live="polite">Loading administrator session…</p></main>;
  if (state.kind === "setup-required") return <SetupPage onSetup={setup} />;
  if (state.kind === "anonymous") return <LoginPage onLogin={login} sessionError={sessionError} />;
  return <AuthenticatedShell history={history} />;
}

export function App({ backend, history }: { backend?: AdminBackend; history?: AdminHistory } = {}) {
  return <SessionProvider backend={backend} history={history}><SessionScreen history={history} /></SessionProvider>;
}
