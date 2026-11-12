import { parseJson, stringifyJson } from "../api/http";
import { createStore, readStorage, writeStorage } from "./store";

// Long-running work started from this browser (backup jobs, file migration, schema
// import, collection apply and "Run now" on a scheduled job). Kept in localStorage so a reload or another tab
// re-attaches; an entry leaves only on a definite outcome (terminal status or 404).

export type OperationKind = "backup" | "restore" | "migration" | "import" | "schema" | "job";

export interface TrackedOperation {
  kind: OperationKind;
  /** Server id of the job, migration or operation. */
  id: string;
  /** Collection name for schema applies; the backup id for backup jobs; the job name for scheduled jobs. */
  subject?: string;
  /** Collection apply: the replay identity and the plan it belongs to. */
  idempotencyKey?: string;
  checksum?: string;
  /** Scheduled job: its lastStartedAt when "Run now" was pressed (a new value means the run started; no clock comparison). */
  since?: string;
  startedAt: number;
}

const storageKey = "pgbase.operations";
// Undecided schema claims expire server-side after about an hour; anything older is stale.
const maxAge = 24 * 60 * 60 * 1000;

function load(): TrackedOperation[] {
  try {
    const list = parseJson(readStorage(storageKey) ?? "[]");
    if (!Array.isArray(list)) return [];
    return (list as TrackedOperation[]).filter((item) => item && typeof item.id === "string" && Date.now() - item.startedAt < maxAge);
  } catch {
    return [];
  }
}

const store = createStore<TrackedOperation[]>(load());

function save(next: TrackedOperation[]) {
  writeStorage(storageKey, stringifyJson(next));
  store.set(next);
}

// Another tab started or finished something.
if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === storageKey) store.set(load());
  });
}

export const useOperations = store.use;
export const operations = store.get;

export function trackOperation(operation: Omit<TrackedOperation, "startedAt">): void {
  const rest = store.get().filter((item) => item.id !== operation.id);
  save([...rest, { ...operation, startedAt: Date.now() }]);
}

/** Removes an entry; true when this call removed it (so exactly one caller reports the outcome). */
export function untrackOperation(id: string): boolean {
  const current = store.get();
  if (!current.some((item) => item.id === id)) return false;
  save(current.filter((item) => item.id !== id));
  return true;
}

export const findOperation = (kind: OperationKind, subject?: string) =>
  store.get().find((item) => item.kind === kind && (subject === undefined || item.subject === subject));

// Operations a screen is settling itself; the navigation feed shows them but leaves the outcome to that screen.
const watched = new Set<string>();

export function watchOperation(id: string): () => void {
  watched.add(id);
  return () => {
    watched.delete(id);
    // Re-render the feed so it starts polling what the screen left open.
    store.set([...store.get()]);
  };
}

export const isWatched = (id: string) => watched.has(id);

type OperationState = "running" | "attention" | "done" | "failed" | "gone";

interface StatusPayload {
  id?: string;
  status?: string;
  phase?: string;
  copiedObjects?: number;
  totalObjects?: number;
  /** Scheduled jobs. */
  running?: boolean;
  failed?: boolean;
  lastStartedAt?: string;
}

// A request whose response was lost may still be on its way, and its replay identity must survive meanwhile.
const replayWindow = 65 * 60 * 1000;
export const startWindow = 2 * 60 * 1000;

/** Where an operation stands, read from its status payload; `notFound` is a 404 for its id. */
export function operationState(operation: TrackedOperation, data: StatusPayload | null | undefined, notFound = false, now = Date.now()): { state: OperationState; progress?: string } {
  const { kind } = operation;
  const age = now - operation.startedAt;
  if (notFound) return operation.idempotencyKey && age < replayWindow ? { state: "attention" } : { state: "gone" };
  if (data === undefined) return { state: "running" };
  // The migration query falls back to the latest one; another migration says nothing about this start yet.
  if (data !== null && kind === "migration" && data.id !== operation.id) return age < startWindow ? { state: "running" } : { state: "gone" };
  if (data === null) return { state: "gone" };
  if (kind === "job") {
    if (data.running) return { state: "running" };
    // The worker picks a requested run up on its next tick; until lastStartedAt moves, it has not started.
    const started = data.lastStartedAt !== undefined && data.lastStartedAt !== operation.since;
    if (!started) return age < startWindow ? { state: "running" } : { state: "gone" };
    return { state: data.failed ? "failed" : "done" };
  }
  const status = data.status ?? "";
  if (kind === "backup" || kind === "restore") {
    if (status === "completed") return { state: "done" };
    if (status === "failed") return { state: "failed" };
    const copying = /^copying_objects_(\d+)_of_(\d+)$/.exec(data.phase ?? "");
    return { state: "running", progress: copying && Number(copying[2]) > 0 ? `${Math.floor((Number(copying[1]) / Number(copying[2])) * 100)}%` : undefined };
  }
  if (kind === "migration") {
    if (status === "committed") return { state: "done" };
    if (status === "failed" || status === "canceled") return { state: "failed" };
    if (status === "preview") return { state: "gone" };
    return { state: "running", progress: data.totalObjects ? `${data.copiedObjects ?? 0}/${data.totalObjects}` : undefined };
  }
  // Schema import and collection apply share the ledger vocabulary.
  if (status === "committed") return { state: "done" };
  if (status === "failed") return { state: "failed" };
  // "unknown" is a claim the server has not started: only resending the same request resolves it.
  if (status === "unknown") return { state: "attention" };
  return { state: "running" };
}
