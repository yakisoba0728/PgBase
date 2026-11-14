import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { settingsQuery } from "../system/queries";
import { errorText } from "../app/queries";
import { Skeleton } from "../components/Feedback";
import { useT } from "../i18n";
import { logStatsQuery } from "./queries";
import type { RequestLogFilter } from "../api/logs";
import { useTimeFormat } from "../app/format";

const classes = ["1", "2", "3", "4", "5"] as const;
const tone: Record<string, string | undefined> = { "4": "warning", "5": "danger" };
const barWidth = 28;
const gap = 10;
const height = 44;

/** Totals for the list's time window (other filters do not narrow them). Bars are status classes; a click filters the list. */
export function LogStats({ range, active, onPick }: { range: Pick<RequestLogFilter, "from" | "to">; active: string; onPick: (statusClass: string) => void }) {
  const t = useT();
  const time = useTimeFormat();
  const stats = useQuery(logStatsQuery(range));
  const settings = useQuery({ ...settingsQuery, refetchOnMount: true, staleTime: 60_000 });
  const retention = settings.data?.logs.retentionDays;
  const [hover, setHover] = useState<string | null>(null);
  if (stats.isError && !stats.data) return <p className="ops-stats txt-hint">{errorText(t, stats.error)}</p>;
  const data = stats.data;
  if (!data) return <div className="ops-stats"><Skeleton variant="cards" /></div>;
  const counts = classes.map((c) => data.statusClasses[`${c}xx`]);
  // Heights only; the shown numbers stay exactly as received.
  const max = Math.max(1, ...counts.map((count) => Number(count ?? 0)));
  const shown = hover ?? active;
  const shownCount = shown ? String(data.statusClasses[`${shown}xx`] ?? 0) : null;
  return (
    <section className="ops-stats" aria-label={t("ops.logs.stats")}>
      <svg className="ops-bars" width={classes.length * (barWidth + gap) - gap} height={height + 16} role="group" aria-label={t("ops.logs.byStatus")}>
        {classes.map((c, index) => {
          const count = counts[index];
          const value = Number(count ?? 0);
          const barHeight = value ? Math.max(3, Math.round((value / max) * height)) : 1;
          const x = index * (barWidth + gap);
          return (
            <g
              key={c}
              className="ops-bar"
              data-tone={tone[c]}
              data-active={active === c ? "" : undefined}
              role="button"
              tabIndex={0}
              aria-label={`${c}xx: ${String(count ?? 0)}`}
              aria-pressed={active === c}
              onClick={() => onPick(active === c ? "" : c)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onPick(active === c ? "" : c);
                }
              }}
              onPointerEnter={() => setHover(c)}
              onPointerLeave={() => setHover(null)}
              onFocus={() => setHover(c)}
              onBlur={() => setHover(null)}
            >
              <rect className="ops-bar-hit" x={x - gap / 2} y={0} width={barWidth + gap} height={height + 16} />
              <rect className="ops-bar-fill" x={x} y={height - barHeight} width={barWidth} height={barHeight} rx={4} />
              <text x={x + barWidth / 2} y={height + 13} textAnchor="middle">{c}xx</text>
            </g>
          );
        })}
      </svg>
      <dl className="ops-figures">
        <div>
          <dt>{shown ? `${shown}xx` : t("ops.logs.total")}</dt>
          <dd className="mono tick" key={shown ? `${shown}:${shownCount}` : String(data.total)}>{shown ? shownCount : String(data.total)}</dd>
        </div>
        <div>
          <dt>{t("ops.logs.avg")}</dt>
          <dd className="mono tick" key={data.averageDurationMs.toFixed(1)}>{data.averageDurationMs.toFixed(1)} ms</dd>
        </div>
        <div>
          <dt>{t("ops.logs.span")}</dt>
          <dd>{data.oldestAt ? `${time(data.oldestAt)} – ${time(data.newestAt)}` : t("common.na")}</dd>
        </div>
        {retention !== undefined && (
          <div>
            <dt>{t("ops.logs.retention")}</dt>
            <dd><Link to="/settings" title={t("ops.logs.retentionEdit")}>{t("ops.logs.retentionDays", { count: retention })}</Link></dd>
          </div>
        )}
        {String(data.dropped) !== "0" && (
          <div data-tone="warning">
            <dt>{t("ops.logs.dropped")}</dt>
            <dd className="mono">{String(data.dropped)}</dd>
          </div>
        )}
      </dl>
    </section>
  );
}
