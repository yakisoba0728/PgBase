import { useEffect } from "react";
import { useBlocker } from "react-router";
import { ConfirmDialog } from "../components/Dialog";
import { useT } from "../i18n";
import { drafts, dropDraft, isDirty } from "./drafts";

interface Props {
  draftKey: string;
  /** Navigation inside this path keeps the draft without asking. */
  basePath: string;
}

function ActiveGuard({ draftKey, basePath }: Props) {
  const t = useT();
  // shortcut: react-router honours one blocker; a dirty record form on the Records tab takes over while it is open.
  const blocker = useBlocker(({ nextLocation }) => isDirty(drafts.get()[draftKey]) && nextLocation.pathname !== basePath && !nextLocation.pathname.startsWith(`${basePath}/`));
  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, []);
  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      title={t("schema.leaveTitle")}
      message={t("schema.leaveText")}
      confirmLabel={t("schema.discard")}
      danger
      onConfirm={() => {
        dropDraft(draftKey);
        blocker.proceed?.();
      }}
      onCancel={() => blocker.reset?.()}
    />
  );
}

/** Guards an unsaved schema draft while another tab of its collection is open (the Schema tab guards itself). */
export function SchemaDraftGuard(props: Props) {
  const entries = drafts.use();
  return isDirty(entries[props.draftKey]) ? <ActiveGuard {...props} /> : null;
}
