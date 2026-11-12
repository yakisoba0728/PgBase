import { BackupStoragePage } from "./pages/backup-storage-page";
import { StorageSettingsPage } from "./pages/storage-settings-page";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { SessionProvider, useBackend, useSession } from "./auth/session";
import { adminRouteIdentity, useAdminLocation, type AdminHistory, type AdminRoute } from "./routing/admin-location";
import { UnsavedDrafts, draftLedger } from "./routing/unsaved-draft";
import type { AdminBackend } from "./backend/ports";
import { routeAvailable } from "./backend/capabilities";
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
import { AdministratorsPage } from "./pages/administrators-page";
import { useAdminExtensionScreens, extensionScreen } from "./extensions/admin-extensions";
import { ExtensionScreen } from "./extensions/extension-screen";

import { WorkspaceContextNavigation, WorkspaceHeader, WorkspaceNavigation, workspaceLabel } from "./components/workspace-chrome";
import { applyWorkspaceTheme, resetWorkspaceTheme } from "./components/workspace-theme";

type Navigate = (route: AdminRoute) => void;

// The route identity keys the outlet, so leaving a resource drops its page
// state before the next one renders instead of each page unwinding it.
function outlet(route: AdminRoute, key: string, navigate: Navigate, sqlConsole?: SQLConsolePort) {
  if (route.kind === "collection-create") return <CollectionDesignerPage key={key} mode={{ kind: "create" }} navigate={navigate} />;
  if (route.kind === "collection-duplicate") return <CollectionDesignerPage key={key} mode={{ kind: "duplicate", collection: route.collection }} navigate={navigate} />;
  if (route.kind === "collection-scaffold") return <CollectionDesignerPage key={key} mode={{ kind: "scaffold", scaffold: route.scaffold }} navigate={navigate} />;
  if (route.kind === "backup-storage") return <BackupStoragePage key={key} />;
  if (route.kind === "storage") return <StorageSettingsPage key={key} />;
  if (route.kind === "operations") return <SchemaOperationsPage key={key} />;
	if (route.kind === "sql-console" && sqlConsole !== undefined) return <SQLConsolePage key={key} port={sqlConsole} />;
  if (route.kind === "system") return <SystemPage key={key} />;
  if (route.kind === "settings") return <SettingsPage key={key} />;
  if (route.kind === "users") return <UsersPage key={key} />;
  if (route.kind === "administrators") return <AdministratorsPage key={key} />;
  if (route.kind === "auth-collections") return <AuthCollectionsPage key={key} />;
  if (route.kind === "service-tokens") return <ServiceTokensPage key={key} />;
  if (route.kind === "schema-transfer") return <SchemaTransferPage key={key} />;
	if (route.kind === "extension") {
		const screen = extensionScreen(route.id, route.path);
		return screen === undefined ? <section><h2>Extension screen unavailable</h2><p role="alert">This extension is not available. Reload the administrator workspace.</p></section> : <ExtensionScreen key={key} screen={screen} />;
	}
  if (route.kind === "collection-edit") return <CollectionDesignerPage key={key} mode={{ kind: "edit", collection: route.collection }} navigate={navigate} />;
  if (route.kind === "records") return <RecordsPage collectionName={route.collection} key={key} navigate={navigate} />;
  if (route.kind === "record") return <RecordEditPage collectionName={route.collection} id={route.id} key={key} navigate={navigate} />;
  return <CollectionsPage key={key} navigate={navigate} />;
}


function AuthenticatedShell({ history, sqlConsole }: { history?: AdminHistory; sqlConsole?: SQLConsolePort }) {
  const { logout, state } = useSession();
	const backend = useBackend();
  const { settings } = backend;
  const [drafts] = useState(draftLedger);
  const logoutInFlight = useRef(false);
  const navigationToggle = useRef<HTMLButtonElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const returnNavigationFocus = useRef(false);
  const focusAcceptedRoute = useRef(false);
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
	const extensionScreens = useAdminExtensionScreens();
	useEffect(() => {
		if (!settings) return;
		const controller = new AbortController();
		void settings.get(controller.signal).then((value) => { if (!controller.signal.aborted) { applyWorkspaceTheme(value.application); } }).catch(() => { /* the workspace remains usable when optional settings are unavailable */ });
		return () => controller.abort();
	}, [settings]);
  // The ref closes navigation synchronously with starting the request; the
  // rendered pending state then makes every control and link inert until it
  // either fails or this shell unmounts after success.
  const { identity, navigate, route } = useAdminLocation(
    history,
    () => !logoutInFlight.current && drafts.mayLeave(),
  );
  useLayoutEffect(() => {
    if (!navigationOpen && returnNavigationFocus.current) {
      returnNavigationFocus.current = false;
      navigationToggle.current?.focus();
    }
  }, [navigationOpen]);
  useLayoutEffect(() => {
    returnNavigationFocus.current = false;
    focusAcceptedRoute.current = true;
    setNavigationOpen(false);
  }, [identity]);
  useLayoutEffect(() => {
    if (navigationOpen || !focusAcceptedRoute.current) return;
    focusAcceptedRoute.current = false;
    document.scrollingElement?.scrollTo?.({ top: 0, left: 0, behavior: "instant" });
    content.current?.focus({ preventScroll: true });
  }, [identity, navigationOpen]);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(max-width: 760px)");
    const changed = () => { if (!media.matches) setNavigationOpen(false); };
    media.addEventListener("change", changed);
    return () => media.removeEventListener("change", changed);
  }, []);
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
  function closeNavigation() { returnNavigationFocus.current = true; setNavigationOpen(false); }
  function go(next: AdminRoute) {
    if (navigationOpen && adminRouteIdentity(next) === identity) closeNavigation();
    else navigate(next);
  }
  return <main className="shell">
    <a className="skip-link" href="#workspace-content">Skip to content</a>
    <WorkspaceHeader email={state.administrator.email} route={route} navigate={go} navigationOpen={navigationOpen} pending={logoutPending} onToggleNavigation={() => setNavigationOpen(true)} onSignOut={() => void signOut()} toggleRef={navigationToggle} />
    <fieldset aria-label="Administrator workspace" className="shell-workspace" disabled={logoutPending} inert={logoutPending}>
      <WorkspaceNavigation route={route} open={navigationOpen} onClose={closeNavigation} navigate={go} extensions={extensionScreens} sqlEnabled={state.sqlConsoleEnabled === true && sqlConsole !== undefined} />
      <div ref={content} role="region" aria-label={`${workspaceLabel(route)} workspace`} inert={navigationOpen} className="workspace-content" id="workspace-content" tabIndex={-1}>
        <WorkspaceContextNavigation route={route} navigate={go} />
        {logoutError && <p className="form-error" role="alert">{logoutError}</p>}
        <UnsavedDrafts ledger={drafts}><div className="route-view" key={identity}>{routeAvailable(backend, route) ? outlet(route, identity, navigate, state.sqlConsoleEnabled === true ? sqlConsole : undefined) : <section><h2>Feature unavailable</h2><p>This server does not support this page.</p></section>}</div></UnsavedDrafts>
      </div>
    </fieldset>
  </main>;
}

function SessionScreen({ history, sqlConsole }: { history?: AdminHistory; sqlConsole?: SQLConsolePort }) {
  const { login, sessionError, setup, state } = useSession();
	const backend = useBackend();
  useLayoutEffect(() => {
    if (state.kind !== "authenticated") resetWorkspaceTheme();
  }, [state.kind]);
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
