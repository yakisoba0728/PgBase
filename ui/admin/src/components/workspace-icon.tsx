import type { ReactNode } from "react";

export type WorkspaceIconName = "database" | "users" | "key" | "shield" | "folder" | "backup" | "transfer" | "activity" | "settings" | "terminal" | "plus" | "search" | "arrow" | "layers" | "menu";
const paths: Record<WorkspaceIconName, ReactNode> = {
  database: <><ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v7c0 4 16 4 16 0V5M4 12v7c0 4 16 4 16 0v-7" /></>,
  users: <><circle cx="9" cy="8" r="3" /><path d="M3 21v-3a6 6 0 0 1 12 0v3M16 5a3 3 0 0 1 0 6M18 15a5 5 0 0 1 3 5" /></>,
  key: <><circle cx="8" cy="8" r="5" /><path d="m12 12 9 9m-3-3 3-3m-6 0 3-3" /></>,
  shield: <><path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z" /><path d="m8 12 3 3 5-6" /></>,
  folder: <path d="M3 7V4h7l2 3h9v13H3V7Z" />,
  backup: <><path d="M4 10a8 8 0 1 1 0 5M4 4v6h6M12 7v5l3 2" /></>,
  transfer: <><path d="M4 7h16l-4-4M20 17H4l4 4M20 7l-4 4M4 17l4-4" /></>,
  activity: <path d="M2 12h5l3-8 4 16 3-8h5" />,
  settings: <><path d="m9 3-1 3-3 1 1 3-2 2 2 2-1 3 3 1 1 3h6l1-3 3-1-1-3 2-2-2-2 1-3-3-1-1-3H9Z" /><circle cx="12" cy="12" r="3" /></>,
  terminal: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 8 4 4-4 4m7 0h3" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  search: <><circle cx="10" cy="10" r="6" /><path d="m15 15 5 5" /></>,
  arrow: <path d="M5 12h14m-5-5 5 5-5 5" />,
  layers: <><path d="m12 3 10 6-10 6L2 9l10-6ZM2 13l10 6 10-6M2 17l10 6 10-6" /></>,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
};
export function WorkspaceIcon({ name, className = "" }: { name: WorkspaceIconName; className?: string }) {
  return <svg aria-hidden="true" className={`workspace-icon ${className}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}
