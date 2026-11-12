import { RiCloseLine } from "@remixicon/react";
import { createStore } from "../app/store";
import { useT } from "../i18n";
import { IconButton } from "./Button";

interface ToastItem {
  id: number;
  message: string;
  tone: "info" | "danger";
}

const store = createStore<ToastItem[]>([]);
let sequence = 0;

function dismiss(id: number) {
  store.set(store.get().filter((item) => item.id !== id));
}

export function toast(message: string, tone: ToastItem["tone"] = "info"): void {
  const id = ++sequence;
  store.set([...store.get(), { id, message, tone }]);
  setTimeout(() => dismiss(id), 5000);
}

export function ToastHost() {
  const t = useT();
  const items = store.use();
  return (
    <section className="toasts" role="status" aria-live="polite" aria-label={t("common.notifications")}>
      {items.map((item) => (
        <div key={item.id} className={item.tone === "danger" ? "toast toast-danger" : "toast"}>
          <p>{item.message}</p>
          <IconButton label={t("common.dismiss")} icon={RiCloseLine} onClick={() => dismiss(item.id)} />
        </div>
      ))}
    </section>
  );
}
