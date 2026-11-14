import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router";
import { RiPuzzle2Line } from "@remixicon/react";
import { EmptyState, ErrorState } from "../components/Feedback";
import { Page } from "../components/Page";
import { useT } from "../i18n";
import { createStore } from "./store";

interface ExtensionScreen {
  id: string;
  label: string;
  /** Accepted for the documented call shape; the screen always lives at /admin/ext/{id}. */
  path?: string;
  /** Renders into the container; may return a cleanup function. */
  mount: (container: HTMLElement) => void | (() => void);
}

declare global {
  interface Window {
    pgbaseAdmin: { registerScreen: (screen: ExtensionScreen) => void };
  }
}

const store = createStore<ExtensionScreen[]>([]);
export const useExtensionScreens = store.use;

export function registerScreen(screen: ExtensionScreen): void {
  if (!screen || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(screen.id)) throw new TypeError("pgbaseAdmin: id must be letters, digits, dashes or underscores");
  if (typeof screen.label !== "string" || !screen.label) throw new TypeError("pgbaseAdmin: label is required");
  if (typeof screen.mount !== "function") throw new TypeError("pgbaseAdmin: mount must be a function");
  if (store.get().some((existing) => existing.id === screen.id)) throw new Error(`pgbaseAdmin: screen "${screen.id}" is already registered`);
  store.set([...store.get(), { id: screen.id, label: screen.label, mount: screen.mount }]);
}

export function ExtensionScreenPage() {
  const t = useT();
  const { id } = useParams();
  const screen = useExtensionScreens().find((candidate) => candidate.id === id);
  const container = useRef<HTMLDivElement>(null);
  const [failure, setFailure] = useState<{ error: unknown } | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const element = container.current;
    if (!screen || !element) return;
    setFailure(null);
    let cleanup: void | (() => void);
    try {
      cleanup = screen.mount(element);
    } catch (error) {
      console.error(`pgbaseAdmin: screen "${screen.id}" failed to mount`, error);
      setFailure({ error });
    }
    return () => {
      if (typeof cleanup === "function") cleanup();
      element.replaceChildren();
    };
  }, [screen, attempt]);

  if (!screen) {
    return (
      <Page title={id}>
        <EmptyState icon={RiPuzzle2Line}>{t("ext.missing")}</EmptyState>
      </Page>
    );
  }
  return (
    <Page title={screen.label}>
      {failure && <ErrorState error={failure.error} onRetry={() => setAttempt((count) => count + 1)} />}
      <div ref={container} className="page-content" hidden={failure !== null} />
    </Page>
  );
}
