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
}

/** Groups after Data (which lists collections); the router gives each entry a route. */
export const navGroups: { label: MessageKey; items: NavEntry[] }[] = [
  {
    label: "nav.auth",
    items: [
      { path: "auth/collections", label: "nav.authCollections", icon: RiShieldUserLine },
      { path: "auth/users", label: "nav.users", icon: RiUser3Line },
    ],
  },
  {
    label: "nav.operations",
    items: [
      { path: "logs", label: "nav.logs", icon: RiFileList3Line },
      { path: "jobs", label: "nav.jobs", icon: RiTimerLine },
      { path: "backups", label: "nav.backups", icon: RiArchive2Line },
      { path: "realtime", label: "nav.realtime", icon: RiBroadcastLine },
    ],
  },
  {
    label: "nav.system",
    items: [
      { path: "settings", label: "nav.settings", icon: RiSettings3Line },
      { path: "storage", label: "nav.storage", icon: RiHardDrive3Line },
      { path: "service-tokens", label: "nav.tokens", icon: RiKey2Line },
      { path: "schema-transfer", label: "nav.schemaTransfer", icon: RiExchangeLine },
      { path: "schema-operations", label: "nav.schemaOperations", icon: RiHistoryLine },
      { path: "sql", label: "nav.sql", icon: RiTerminalBoxLine },
      { path: "administrators", label: "nav.administrators", icon: RiAdminLine },
    ],
  },
];
