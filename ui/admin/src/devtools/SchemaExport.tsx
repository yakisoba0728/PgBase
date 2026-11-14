import { CopyButton } from "../components/CopyButton";
import { useQuery } from "@tanstack/react-query";
import { RiDownloadLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import { Button } from "../components/Button";
import { QueryView } from "../components/QueryView";
import { Checkbox } from "../components/Field";
import { useT } from "../i18n";
import { useSearchState } from "../app/search";
import { downloadJson } from "../app/download";
import { pickCollections } from "./model";
import { exportQuery } from "./queries";
import type { SchemaDocument } from "../api/schema";
import "../styles/devtools.css";

/** Collection picker on the left, the exported document on the right. Skipped names live in ?skip=. */
export function SchemaExport() {
  const query = useQuery(exportQuery);
  // A column, so a "couldn't refresh" notice sits above the picker rather than beside it.
  return <div className="page-form"><QueryView query={query} skeleton="editor">{(schema) => <ExportPicker schema={schema} />}</QueryView></div>;
}

function ExportPicker({ schema: loaded }: { schema: SchemaDocument }) {
  const t = useT();
  const { params, update } = useSearchState();
  // An empty project exports `collections: null`.
  const data = { ...loaded, collections: loaded.collections ?? [] };
  const names = data.collections.map((collection) => collection.name);
  const skip = (params.get("skip") ?? "").split(",").filter((name) => names.includes(name));
  const setSkip = (next: string[]) => update({ skip: next.join(",") || null });
  const exported = pickCollections(data, skip);
  const text = stringify(exported, null, 2) ?? "";
  const picked = names.length - skip.length;


  return (
    <div className="dt-transfer">
      <aside className="dt-side" aria-label={t("devtools.export.collections")}>
        <div className="dt-side-head">
          <span>{t("devtools.export.picked", { count: picked, total: names.length })}</span>
          <Checkbox
            label={t("devtools.export.all")}
            checked={skip.length === 0}
            onChange={() => setSkip(skip.length === 0 ? names : [])}
          />
        </div>
        {names.length === 0 ? (
          <p className="dt-muted">{t("collections.empty")}</p>
        ) : (
          <div className="dt-checks">
            {names.map((name) => (
              <Checkbox
                key={name}
                label={<span className="mono">{name}</span>}
                checked={!skip.includes(name)}
                onChange={(event) => setSkip(event.target.checked ? skip.filter((other) => other !== name) : [...skip, name])}
              />
            ))}
          </div>
        )}
        {(loaded.authCollections?.length ?? 0) > 0 && <p className="dt-muted">{t("devtools.export.authKept", { count: loaded.authCollections?.length ?? 0 })}</p>}
      </aside>
      <section className="dt-main" aria-label={t("devtools.export.document")}>
        <div className="toolbar toolbar-inset">
          <span className="dt-muted">{t("devtools.export.size", { lines: text.split("\n").length })}</span>
          <div className="actions">
            <CopyButton text value={text} />
            <Button size="sm" icon={RiDownloadLine} onClick={() => downloadJson("pgbase-schema.json", exported)}>{t("devtools.download")}</Button>
          </div>
        </div>
        <pre className="dt-code mono" tabIndex={0} aria-label={t("devtools.export.document")}>{text}</pre>
      </section>
    </div>
  );
}
