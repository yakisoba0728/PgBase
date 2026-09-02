import type { OperationReceipt, OperationStatus } from "../backend/ports";
import { operationFailureMessage } from "../operations/operation-recovery";
import { useOperationHistory } from "../operations/operation-history";

// `running` and `unknown` are not two shades of failure and not a quiet
// success: both say the ledger has not decided, and the wording has to keep
// saying so.
const statusText: Record<OperationStatus, string> = {
  running: "Still applying",
  unknown: "Outcome not yet known",
  committed: "Applied",
  failed: "Not applied",
};

const operationText: Record<string, string> = {
  collection_create: "Create collection",
  collection_update: "Update collection",
  collection_delete: "Delete collection",
};

function OperationRow({ receipt }: { receipt: OperationReceipt }) {
  const undecided = receipt.status === "running" || receipt.status === "unknown";
  return <li>
    <span className="operation-status" data-status={receipt.status}>{statusText[receipt.status] ?? receipt.status}</span>
    <span className="operation-kind">{operationText[receipt.operation] ?? receipt.operation}</span>
    <span className="operation-collection">{receipt.collectionId}</span>
    <span className="operation-version">
      {receipt.committedVersion === undefined ? `from version ${receipt.baseVersion}` : `version ${receipt.baseVersion} → ${receipt.committedVersion}`}
    </span>
    <time dateTime={receipt.createdAt}>{receipt.createdAt}</time>
    {receipt.status === "failed" && <span className="operation-failure">{operationFailureMessage(receipt.failureCode, "The change did not apply.")}</span>}
    {undecided && <span className="operation-failure">This operation has not been decided yet. Check again before retrying it.</span>}
  </li>;
}

export function SchemaOperationsPage() {
  const history = useOperationHistory();
  return <section>
    <h2>Schema operations</h2>
    <p>The most recent schema operations recorded for your administrator account, newest first.</p>
    {history.phase === "loading" && <p aria-live="polite">Loading schema operations…</p>}
    {history.error !== null && <p role="alert">{history.error}</p>}
    {history.phase === "ready" && history.items.length === 0 && <p>No schema operations yet.</p>}
    {history.items.length > 0 && <ol className="operation-list">
      {history.items.map((receipt) => <OperationRow key={receipt.operationId} receipt={receipt} />)}
    </ol>}
    {history.hasMore && <button disabled={history.loadingMore} onClick={history.loadMore} type="button">
      {history.loadingMore ? "Loading…" : "Next page"}
    </button>}
  </section>;
}
