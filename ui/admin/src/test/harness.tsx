import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { App } from "../app";
import { SessionProvider } from "../auth/session";
import type { AdminBackend, CollectionDefinition, FieldDefinition } from "../backend/ports";
import { backendScript, type BackendScript } from "./backend-script";
import { memoryBackend } from "./memory-backend";
import { memoryHistory, type MemoryHistory } from "./memory-history";

export const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };

export function activeSessionExpiresAt(): string {
  return new Date(Date.now() + 60 * 60 * 1000).toISOString();
}

/** Scripts the session bootstrap every authenticated surface needs first. */
export function signedInScript(): BackendScript {
  const script = backendScript();
  script.resume.push({ value: { kind: "authenticated", session: { administrator, expiresAt: activeSessionExpiresAt() } } });
  return script;
}

export function textField(name: string, ordinal: number, patch: Partial<FieldDefinition> = {}): FieldDefinition {
  return { name, type: "text", required: false, unique: false, indexed: true, ordinal, ...patch };
}

export function collection(name: string, id: string, fields: FieldDefinition[] = []): CollectionDefinition {
  return { id, name, tableName: name, fields };
}

export function sessionWrapper(backend: AdminBackend, history: MemoryHistory) {
  return ({ children }: { children: ReactNode }) => <SessionProvider backend={backend} history={history}>{children}</SessionProvider>;
}

export interface AdminHarness { script: BackendScript; history: MemoryHistory; backend: AdminBackend }

export function scriptedAdmin(path = "/admin/collections", script = signedInScript()): AdminHarness {
  return { script, history: memoryHistory(path), backend: memoryBackend(script) };
}

export function renderAdmin(harness: AdminHarness) {
  return render(<App backend={harness.backend} history={harness.history} />);
}
