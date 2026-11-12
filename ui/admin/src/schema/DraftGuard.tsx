import { outside, UnsavedGuard } from "../components/UnsavedGuard";
import { drafts, dropDraft, isDirty } from "./drafts";

/** Guards an unsaved schema draft while another tab of its collection is open (the Schema tab guards itself). */
export function SchemaDraftGuard({ draftKey, basePath }: { draftKey: string; basePath: string }) {
  const entries = drafts.use();
  return isDirty(entries[draftKey]) ? <UnsavedGuard dirty leaves={outside(basePath)} onDiscard={() => dropDraft(draftKey)} /> : null;
}
