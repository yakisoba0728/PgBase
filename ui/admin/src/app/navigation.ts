import {
  RiAdminLine,
  RiArchive2Line,
  RiBroadcastLine,
  RiExchangeLine,
  RiFileList3Line,
  RiHardDrive3Line,
  RiHistoryLine,
  RiKey2Line,
  RiSettings3Line,
  RiShieldUserLine,
  RiTerminalBoxLine,
  RiTimerLine,
  RiUser3Line,
  type RemixiconComponentType,
} from "@remixicon/react";
import type { MessageKey } from "../i18n";

interface NavEntry {
  path: string;
  label: MessageKey;
  icon: RemixiconComponentType;
  /** "g" then this key goes here. */
  key: string;
}

/** Groups after Data (which lists collections); the router gives each entry a route. */
export const navGroups: { label: MessageKey; items: NavEntry[] }[] = [
  {
    label: "nav.auth",
    items: [
      { path: "auth/collections", label: "nav.authCollections", icon: RiShieldUserLine, key: "a" },
      { path: "auth/users", label: "nav.users", icon: RiUser3Line, key: "u" },
    ],
  },
  {
    label: "nav.operations",
    items: [
      { path: "logs", label: "nav.logs", icon: RiFileList3Line, key: "l" },
      { path: "jobs", label: "nav.jobs", icon: RiTimerLine, key: "j" },
      { path: "backups", label: "nav.backups", icon: RiArchive2Line, key: "b" },
      { path: "realtime", label: "nav.realtime", icon: RiBroadcastLine, key: "r" },
    ],
  },
  {
    label: "nav.system",
    items: [
      { path: "settings", label: "nav.settings", icon: RiSettings3Line, key: "s" },
      { path: "storage", label: "nav.storage", icon: RiHardDrive3Line, key: "f" },
      { path: "service-tokens", label: "nav.tokens", icon: RiKey2Line, key: "t" },
      { path: "schema-transfer", label: "nav.schemaTransfer", icon: RiExchangeLine, key: "x" },
      { path: "schema-operations", label: "nav.schemaOperations", icon: RiHistoryLine, key: "o" },
      { path: "sql", label: "nav.sql", icon: RiTerminalBoxLine, key: "q" },
      { path: "administrators", label: "nav.administrators", icon: RiAdminLine, key: "m" },
    ],
  },
];

/** Sub-views that are routes of their own; the pages render them as tabs and the command palette lists them. */
export const screenTabs: Record<"settings" | "storage" | "schemaTransfer", { path: string; label: MessageKey }[]> = {
  settings: [
    { path: "/settings", label: "system.tab.settings" },
    { path: "/settings/status", label: "system.tab.status" },
  ],
  storage: [
    { path: "/storage", label: "storage.tab.files" },
    { path: "/storage/migration", label: "storage.tab.migration" },
    { path: "/storage/backups", label: "storage.tab.backups" },
  ],
  schemaTransfer: [
    { path: "/schema-transfer", label: "devtools.export.tab" },
    { path: "/schema-transfer/import", label: "devtools.import.tab" },
  ],
};

/** Page tabs for a screen; the first is the screen's own path, matched exactly. */
export const pageTabs = (t: (key: MessageKey) => string, tabs: { path: string; label: MessageKey }[]) =>
  tabs.map((tab, index) => ({ to: tab.path, label: t(tab.label), end: index === 0 }));
