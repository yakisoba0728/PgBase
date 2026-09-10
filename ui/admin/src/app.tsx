import { BackupStoragePage } from "./pages/backup-storage-page";
import { StorageSettingsPage } from "./pages/storage-settings-page";
import { useEffect, useRef, useState } from "react";
import { SessionProvider, useBackend, useSession } from "./auth/session";
import { AdminLink } from "./routing/admin-link";
import { useAdminLocation, type AdminHistory, type AdminRoute } from "./routing/admin-location";
import { UnsavedDrafts, draftLedger } from "./routing/unsaved-draft";
import type { AdminBackend } from "./backend/ports";
import { httpBackend } from "./backend/http-backend";
import { createSQLConsoleBackend, type SQLConsolePort } from "./backend/sql-console-backend";
import { CollectionDesignerPage } from "./pages/collection-designer-page";
import { CollectionsPage } from "./pages/collections-page";
import { RecordEditPage } from "./pages/record-edit-page";
import { RecordsPage } from "./pages/records-page";
import { SchemaOperationsPage } from "./pages/schema-operations-page";
import { SystemPage } from "./pages/system-page";
import { LoginPage } from "./pages/login-page";
import { SetupPage } from "./pages/setup-page";
import { UsersPage } from "./pages/users-page";
import { AuthCollectionsPage } from "./pages/auth-collections-page";
import { ServiceTokensPage } from "./pages/service-tokens-page";
import { SchemaTransferPage } from "./pages/schema-transfer-page";
import { SQLConsolePage } from "./pages/sql-console-page";
import { SettingsPage } from "./pages/settings-page";

type Navigate = (route: AdminRoute) => void;

// The route identity keys the outlet, so leaving a resource drops its page
// state before the next one renders instead of each page unwinding it.
function outlet(route: AdminRoute, key: string, navigate: Navigate, sqlConsole?: SQLConsolePort) {
  if (route.kind === "collection-create") return <CollectionDesignerPage key={key} mode={{ kind: "create" }} navigate={navigate} />;
  if (route.kind === "backup-storage") return <BackupStoragePage key={key} />;
  if (route.kind === "storage") return <StorageSettingsPage key={key} />;
  if (route.kind === "operations") return <SchemaOperationsPage key={key} />;
	if (route.kind === "sql-console" && sqlConsole !== undefined) return <SQLConsolePage key={key} port={sqlConsole} />;
  if (route.kind === "system") return <SystemPage key={key} />;
  if (route.kind === "settings") return <SettingsPage key={key} />;
  if (route.kind === "users") return <UsersPage key={key} />;
  if (route.kind === "auth-collections") return <AuthCollectionsPage key={key} />;
  if (route.kind === "service-tokens") return <ServiceTokensPage key={key} />;
  if (route.kind === "schema-transfer") return <SchemaTransferPage key={key} />;
  if (route.kind === "collection-edit") return <CollectionDesignerPage key={key} mode={{ kind: "edit", collection: route.collection }} navigate={navigate} />;
  if (route.kind === "records") return <RecordsPage collectionName={route.collection} key={key} navigate={navigate} />;
  if (route.kind === "record") return <RecordEditPage collectionName={route.collection} id={route.id} key={key} navigate={navigate} />;
  return <CollectionsPage key={key} navigate={navigate} />;
}

function AuthenticatedShell({ history, sqlConsole }: { history?: AdminHistory; sqlConsole?: SQLConsolePort }) {
  const { logout, state } = useSession();
	const { settings } = useBackend();
  const [drafts] = useState(draftLedger);
  const logoutInFlight = useRef(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
	useEffect(() => {
		if (!settings) return;
		const controller = new AbortController();
		void settings.get(controller.signal).then((value) => { if (!controller.signal.aborted) { document.documentElement.dataset.theme = value.application.theme; document.documentElement.style.setProperty("--accent", value.application.accent); document.title = value.application.branding || "pgBase administration"; } }).catch(() => { /* the workspace remains usable when optional settings are unavailable */ });
		return () => controller.abort();
	}, [settings]);
  // The ref closes navigation synchronously with starting the request; the
  // rendered pending state then makes every control and link inert until it
  // either fails or this shell unmounts after success.
  const { identity, navigate, route } = useAdminLocation(
    history,
    () => !logoutInFlight.current && drafts.mayLeave(),
  );
  if (state.kind !== "authenticated") return null;
  async function signOut() {
    // Signing out ends the session the draft would have been saved under, so it
    // is a departure the administrator is asked about like any other. Keep the
    // guard held until logout actually succeeds: a failed request leaves both
    // the session and its unsaved draft in this shell.
    if (logoutInFlight.current || !drafts.confirmLeave()) return;
    logoutInFlight.current = true;
    setLogoutPending(true);
    setLogoutError(null);
    try {
      await logout();
    } catch {
      logoutInFlight.current = false;
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
    <fieldset aria-label="Administrator workspace" className="shell-workspace" disabled={logoutPending} inert={logoutPending}>
      {logoutError && <p className="form-error" role="alert">{logoutError}</p>}
      <nav aria-label="Administration"><AdminLink className="text-button" navigate={navigate} route={{ kind: "storage" }}>File storage</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "backup-storage" }}>Backup storage</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "collections" }}>Collections</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "schema-transfer" }}>Schema transfer</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "users" }}>Users</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "auth-collections" }}>Auth collections</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "service-tokens" }}>Service tokens</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "operations" }}>Schema operations</AdminLink>{state.sqlConsoleEnabled === true && sqlConsole !== undefined && <AdminLink className="text-button" navigate={navigate} route={{ kind: "sql-console" }}>SQL console</AdminLink>}<AdminLink className="text-button" navigate={navigate} route={{ kind: "settings" }}>Settings</AdminLink><AdminLink className="text-button" navigate={navigate} route={{ kind: "system" }}>System</AdminLink></nav>
      <UnsavedDrafts ledger={drafts}>{outlet(route, identity, navigate, state.sqlConsoleEnabled === true ? sqlConsole : undefined)}</UnsavedDrafts>
    </fieldset>
  </main>;
}

function SessionScreen({ history, sqlConsole }: { history?: AdminHistory; sqlConsole?: SQLConsolePort }) {
  const { login, sessionError, setup, state } = useSession();
	const backend = useBackend();
  if (state.kind === "loading") return <main className="auth-page"><p aria-live="polite">Loading administrator session…</p></main>;
  if (state.kind === "setup-required") return <SetupPage onSetup={setup} onRestoreBackup={backend.session.restoreSetupBackup} onSetupBackupJob={backend.session.setupBackupJob} onUploadBackup={backend.session.uploadSetupBackup} />;
  if (state.kind === "anonymous") return <LoginPage onConfirmPasswordReset={backend.session.confirmPasswordReset} onLogin={login} onRequestPasswordReset={backend.session.requestPasswordReset} sessionError={sessionError} />;
  return <AuthenticatedShell history={history} sqlConsole={sqlConsole} />;
}

export function App({ backend, history, sqlConsole }: { backend?: AdminBackend; history?: AdminHistory; sqlConsole?: SQLConsolePort } = {}) {
	const [runtime] = useState(() => {
		if (backend !== undefined) return { admin: backend, consolePort: sqlConsole };
		const admin = httpBackend();
		return { admin, consolePort: sqlConsole ?? createSQLConsoleBackend(admin.sqlConsoleTransport) };
	});
	return <SessionProvider backend={runtime.admin} history={history}><SessionScreen history={history} sqlConsole={runtime.consolePort} /></SessionProvider>;
}
