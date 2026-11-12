import { CopyButton } from "../components/CopyButton";
import { useQuery } from "@tanstack/react-query";
import { RiDownloadLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import { Button } from "../components/Button";
import { ErrorState, Loader } from "../components/Feedback";
import { Checkbox } from "../components/Field";
import { useT } from "../i18n";
import { useSearchState } from "../app/search";
import { pickCollections } from "./model";
import { exportQuery } from "./queries";

/** Collection picker on the left, the exported document on the right. Skipped names live in ?skip=. */
export function SchemaExport() {
  const t = useT();
  const { params, update } = useSearchState();
  const query = useQuery(exportQuery);
  if (query.isPending) return <Loader />;
  if (query.isError) return <ErrorState error={query.error} onRetry={query.refetch} />;

  const names = query.data.collections.map((collection) => collection.name);
  const skip = (params.get("skip") ?? "").split(",").filter((name) => names.includes(name));
  const setSkip = (next: string[]) => update({ skip: next.join(",") || null });
  const text = stringify(pickCollections(query.data, skip), null, 2) ?? "";
  const picked = names.length - skip.length;

  const download = () => {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([text], { type: "application/json" }));
    link.download = "pgbase-schema.json";
    link.click();
    URL.revokeObjectURL(link.href);
  };

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
        {(query.data.authCollections?.length ?? 0) > 0 && <p className="dt-muted">{t("devtools.export.authKept", { count: query.data.authCollections?.length ?? 0 })}</p>}
      </aside>
      <section className="dt-main" aria-label={t("devtools.export.document")}>
        <div className="toolbar">
          <span className="dt-muted">{t("devtools.export.size", { lines: text.split("\n").length })}</span>
          <div className="dt-actions">
            <CopyButton text value={text} />
            <Button size="sm" icon={RiDownloadLine} onClick={download}>{t("devtools.download")}</Button>
          </div>
        </div>
        <pre className="dt-code mono" tabIndex={0} aria-label={t("devtools.export.document")}>{text}</pre>
      </section>
    </div>
  );
}
