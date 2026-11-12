import { RiCloseLine } from "@remixicon/react";
import { createStore } from "../app/store";
import { useT } from "../i18n";
import { IconButton } from "./Button";

interface ToastAction {
  label: string;
  run: () => void;
}

interface ToastItem {
  id: number;
  message: string;
  tone: "info" | "danger";
  action?: ToastAction;
}

const store = createStore<ToastItem[]>([]);
let sequence = 0;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

function hold(id: number) {
  clearTimeout(timers.get(id));
  timers.delete(id);
}

function dismiss(id: number) {
  hold(id);
  const rest = store.get().filter((item) => item.id !== id);
  store.set(rest);
  // The one waiting underneath shows next, with its full time.
  const next = rest.at(-1);
  if (next && !timers.has(next.id)) schedule(next);
}

function schedule(item: ToastItem) {
  hold(item.id);
  timers.set(item.id, setTimeout(() => dismiss(item.id), item.action ? 8000 : 5000));
}

/**
 * One short message; an action (e.g. Undo) keeps it up a little longer, and hover or focus keeps it up until left.
 * Only the newest shows: an error or an action underneath waits with its timer stopped, so it is never lost unseen;
 * a plain note underneath is replaced.
 */
export function toast(message: string, tone: ToastItem["tone"] = "info", action?: ToastAction): void {
  const waiting = store.get().filter((item) => {
    const keep = item.tone === "danger" || item.action !== undefined;
    hold(item.id);
    return keep;
  });
  const item = { id: ++sequence, message, tone, action };
  store.set([...waiting, item]);
  schedule(item);
}

export function ToastHost() {
  const t = useT();
  const items = store.use();
  return (
    <section className="toasts" role="status" aria-live="polite" aria-label={t("common.notifications")}>
      {items.slice(-1).map((item) => (
        <div
          key={item.id}
          className={item.tone === "danger" ? "toast toast-danger" : "toast"}
          onMouseEnter={() => hold(item.id)}
          onFocus={() => hold(item.id)}
          onMouseLeave={(event) => !event.currentTarget.matches(":focus-within") && schedule(item)}
          onBlur={(event) => !event.currentTarget.matches(":hover") && schedule(item)}
        >
          <p title={item.message}>{item.message}</p>
          {item.action && (
            <button
              type="button"
              className="link toast-action"
              onClick={() => {
                dismiss(item.id);
                item.action?.run();
              }}
            >
              {item.action.label}
            </button>
          )}
          <IconButton label={t("common.dismiss")} icon={RiCloseLine} onClick={() => dismiss(item.id)} />
        </div>
      ))}
    </section>
  );
}
