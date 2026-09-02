import { useState } from "react";
import { SessionProvider, useSession } from "./auth/session";
import { useAdminLocation, type AdminHistory, type AdminRoute } from "./routing/admin-location";
import { CollectionCreatePage } from "./pages/collection-create-page";
import { CollectionEditPage } from "./pages/collection-edit-page";
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
  if (route.kind === "collection-create") return <CollectionCreatePage key={key} navigate={navigate} />;
  if (route.kind === "system") return <SystemPage key={key} />;
  if (route.kind === "collection-edit") return <CollectionEditPage collectionName={route.collection} key={key} navigate={navigate} />;
  if (route.kind === "records") return <RecordsPage collectionName={route.collection} key={key} navigate={navigate} />;
  if (route.kind === "record") return <RecordEditPage collectionName={route.collection} id={route.id} key={key} navigate={navigate} />;
  return <CollectionsPage key={key} navigate={navigate} />;
}

function AuthenticatedShell({ history }: { history?: AdminHistory }) {
  const { logout, state } = useSession();
  const { identity, navigate, route } = useAdminLocation(history);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  if (state.kind !== "authenticated") return null;
  async function signOut() {
    if (logoutPending) return;
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
    <nav aria-label="Administration"><button className="text-button" onClick={() => navigate({ kind: "collections" })} type="button">Collections</button><button className="text-button" onClick={() => navigate({ kind: "system" })} type="button">System</button></nav>
    {outlet(route, identity, navigate)}
  </main>;
}

function SessionScreen({ history }: { history?: AdminHistory }) {
  const { login, sessionError, setup, state } = useSession();
  if (state.kind === "loading") return <main className="auth-page"><p aria-live="polite">Loading administrator session…</p></main>;
  if (state.kind === "setup-required") return <SetupPage onSetup={setup} />;
  if (state.kind === "anonymous") return <LoginPage onLogin={login} sessionError={sessionError} />;
  return <AuthenticatedShell history={history} />;
}

export function App({ history }: { history?: AdminHistory } = {}) {
  return <SessionProvider history={history}><SessionScreen history={history} /></SessionProvider>;
}
