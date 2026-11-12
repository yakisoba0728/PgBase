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
  store.set(store.get().filter((item) => item.id !== id));
}

function schedule(item: ToastItem) {
  hold(item.id);
  timers.set(item.id, setTimeout(() => dismiss(item.id), item.action ? 8000 : 5000));
}

/** One short message; an action (e.g. Undo) keeps it up a little longer, and hover or focus keeps it up until left. */
export function toast(message: string, tone: ToastItem["tone"] = "info", action?: ToastAction): void {
  const item = { id: ++sequence, message, tone, action };
  store.set([...store.get(), item]);
  schedule(item);
}

export function ToastHost() {
  const t = useT();
  const items = store.use();
  return (
    <section className="toasts" role="status" aria-live="polite" aria-label={t("common.notifications")}>
      {items.map((item) => (
        <div
          key={item.id}
          className={item.tone === "danger" ? "toast toast-danger" : "toast"}
          onMouseEnter={() => hold(item.id)}
          onFocus={() => hold(item.id)}
          onMouseLeave={(event) => !event.currentTarget.matches(":focus-within") && schedule(item)}
          onBlur={(event) => !event.currentTarget.matches(":hover") && schedule(item)}
        >
          <p>{item.message}</p>
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
