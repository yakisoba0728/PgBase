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

function dismiss(id: number) {
  store.set(store.get().filter((item) => item.id !== id));
}

/** One short message; an action (e.g. Undo) keeps it up a little longer. */
export function toast(message: string, tone: ToastItem["tone"] = "info", action?: ToastAction): void {
  const id = ++sequence;
  store.set([...store.get(), { id, message, tone, action }]);
  setTimeout(() => dismiss(id), action ? 8000 : 5000);
}

export function ToastHost() {
  const t = useT();
  const items = store.use();
  return (
    <section className="toasts" role="status" aria-live="polite" aria-label={t("common.notifications")}>
      {items.map((item) => (
        <div key={item.id} className={item.tone === "danger" ? "toast toast-danger" : "toast"}>
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
