import { useEffect, useState } from "react";
import { SessionProvider, useSession } from "./auth/session";
import { CollectionCreatePage } from "./pages/collection-create-page";
import { CollectionsPage } from "./pages/collections-page";
import { RecordEditPage } from "./pages/record-edit-page";
import { RecordsPage } from "./pages/records-page";
import { SystemPage } from "./pages/system-page";
import { LoginPage } from "./pages/login-page";
import { SetupPage } from "./pages/setup-page";

function decodePathSegment(value: string | undefined): string | null {
  if (value === undefined) return null;
  try { return decodeURIComponent(value); } catch { return null; }
}

function AuthenticatedShell() {
  const { logout, state } = useSession();
  const [path, setPath] = useState(window.location.pathname);
  useEffect(() => {
    const update = () => setPath(window.location.pathname);
    window.addEventListener("popstate", update);
    return () => window.removeEventListener("popstate", update);
  }, []);
  if (state.kind !== "authenticated") return null;
  function navigate(next: string) {
    window.history.pushState(null, "", next);
    setPath(next);
  }
  let page = <CollectionsPage navigate={navigate} />;
  if (path === "/admin/collections/new") page = <CollectionCreatePage navigate={navigate} />;
  else if (path === "/admin/system") page = <SystemPage />;
  else {
    const match = /^\/admin\/collections\/([^/]+)\/records(?:\/([^/]+))?$/.exec(path);
    if (match) {
      const collectionName = decodePathSegment(match[1]);
      const recordID = match[2] === "new" ? undefined : decodePathSegment(match[2]);
      if (collectionName !== null && match[2] === undefined) page = <RecordsPage collectionName={collectionName} navigate={navigate} />;
      else if (collectionName !== null && recordID !== null) page = <RecordEditPage collectionName={collectionName} id={recordID} navigate={navigate} />;
    }
  }
  return <main className="shell">
    <header>
      <div>
        <h1>pgBase administration</h1>
        <p>{state.administrator.email}</p>
      </div>
      <button onClick={() => void logout()} type="button">Sign out</button>
    </header>
    <nav aria-label="Administration"><button className="text-button" onClick={() => navigate("/admin/collections")} type="button">Collections</button><button className="text-button" onClick={() => navigate("/admin/system")} type="button">System</button></nav>
    {page}
  </main>;
}

function SessionScreen() {
  const { login, sessionError, setup, state } = useSession();
  if (state.kind === "loading") return <main className="auth-page"><p aria-live="polite">Loading administrator session…</p></main>;
  if (state.kind === "setup-required") return <SetupPage onSetup={setup} />;
  if (state.kind === "anonymous") return <LoginPage onLogin={login} sessionError={sessionError} />;
  return <AuthenticatedShell />;
}

export function App() {
  return <SessionProvider><SessionScreen /></SessionProvider>;
}
