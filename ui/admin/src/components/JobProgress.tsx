import type { ReactNode } from "react";

interface JobProgressProps {
  title: string;
  /** Current phase or outcome, in words. */
  detail: ReactNode;
  /** Ids and timestamps, muted. */
  meta?: ReactNode;
  state: "running" | "done" | "failed" | "attention";
  /** Known progress; without it the row only spins (no invented percentages). */
  done?: number;
  total?: number;
  error?: string;
  action?: ReactNode;
}

const tones = { running: "accent", done: "success", failed: "danger", attention: "warning" } as const;

/** One long-running server job: what it is, where it stands, measured progress, and its action. */
export function JobProgress({ title, detail, meta, state, done, total, error, action }: JobProgressProps) {
  const measured = total !== undefined && total > 0 && done !== undefined;
  return (
    <section className="job-progress" data-tone={tones[state]} aria-live="polite" aria-label={title}>
      {state === "running" ? <span className="loader" aria-hidden /> : <span className="dot" />}
      <div className="job-progress-text">
        <strong>{title}</strong>
        <span>{detail}</span>
        {meta && <span className="txt-hint mono">{meta}</span>}
        {error && <span className="job-progress-error">{error}</span>}
      </div>
      {measured && (
        <div className="job-progress-bar">
          <progress className="progress-bar" value={done} max={total} />
          <span className="mono">{Math.floor((done / total) * 100)}%</span>
        </div>
      )}
      {action}
    </section>
  );
}
