import { t, useLocale } from "../i18n";
import { useState } from "react";
import { Button, PageHeading } from "../components/ui";
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
  useLocale();
  const undecided = receipt.status === "running" || receipt.status === "unknown";
  return <li>
    <span className="operation-status" data-status={receipt.status}>{t(statusText[receipt.status] ?? receipt.status)}</span>
    <span className="operation-kind">{t(operationText[receipt.operation] ?? receipt.operation)}</span>
    <span className="operation-collection">{receipt.collectionId}</span>
    {/* The identity the apply was sent under, and the only handle an
        administrator has when asking about one particular operation. */}
    <span className="operation-id">{receipt.operationId}</span>
    <span className="operation-version">
      {receipt.committedVersion === undefined ? t("from version {version}", { version: receipt.baseVersion }) : t("version {base} → {committed}", { base: receipt.baseVersion, committed: receipt.committedVersion })}
    </span>
    <time dateTime={receipt.createdAt}>{receipt.createdAt}</time>
    {receipt.status === "failed" && <span className="operation-failure">{t(operationFailureMessage(receipt.failureCode, "The change did not apply."))}</span>}
    {undecided && <span className="operation-failure">{t("This operation has not been decided yet. Check again before retrying it.")}</span>}
  </li>;
}

export function SchemaOperationsPage() {
  useLocale();
  const history = useOperationHistory();
  const [status, setStatus] = useState("");
  const [search, setSearch] = useState("");
  const visible = history.items.filter(item => (!status || item.status === status) && `${item.operationId} ${item.collectionId} ${item.operation}`.toLowerCase().includes(search.trim().toLowerCase()));
  return <section className="management-page schema-operations-page">
    <PageHeading title={t("Schema operations")} description={t("The most recent schema operations recorded for your administrator account, newest first.")} />
    <div className="collection-toolbar"><label className="collection-search">{t("Search operations")}<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder={t("Operation or collection ID\u2026")} /></label><label>{t("Operation status")}<select value={status} onChange={event => setStatus(event.target.value)}><option value="">{t("All statuses")}</option>{Object.entries(statusText).map(([value, label]) => <option key={value} value={value}>{t("{status} operations", { status: t(label) })}</option>)}</select></label></div>
    {history.phase === "loading" && <p aria-live="polite">{t("Loading schema operations…")}</p>}
    {history.error !== null && <p role="alert">{t(history.error)}</p>}
    {history.phase === "ready" && history.items.length === 0 && <p>{t("No schema operations yet.")}</p>}
    {history.items.length > 0 && <ol className="operation-list" aria-label={t("Schema operation history")}>
      {visible.map((receipt) => <OperationRow key={receipt.operationId} receipt={receipt} />)}
    </ol>}
    {history.phase === "ready" && history.items.length > 0 && visible.length === 0 && <p role="status">{t("No loaded operations match these filters. Load the next page to search older operations.")}</p>}
    {history.hasMore && <Button variant="secondary" disabled={history.loadingMore} onClick={history.loadMore} type="button">
      {t(history.loadingMore ? "Loading…" : "Next page")}
    </Button>}
  </section>;
}
