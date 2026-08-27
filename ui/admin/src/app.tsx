import { SessionProvider, useSession } from "./auth/session";
import { LoginPage } from "./pages/login-page";
import { SetupPage } from "./pages/setup-page";

function AuthenticatedShell() {
  const { logout, state } = useSession();
  if (state.kind !== "authenticated") return null;
  return <main className="shell">
    <header>
      <div>
        <h1>pgBase administration</h1>
        <p>{state.administrator.email}</p>
      </div>
      <button onClick={() => void logout()} type="button">Sign out</button>
    </header>
    <section aria-label="Administrator home">
      <h2>Welcome</h2>
      <p>Administration features will appear here.</p>
    </section>
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
