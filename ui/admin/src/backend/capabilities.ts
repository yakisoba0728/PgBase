import type { AdminBackend, AdminCapabilities } from "./ports";
import type { AdminRoute } from "../routing/admin-location";
export function routeAvailable(backend: AdminBackend, route: AdminRoute): boolean {
  const keys: Partial<Record<AdminRoute["kind"], keyof AdminCapabilities>> = { settings: "settings", administrators: "administrators", "schema-transfer": "schemaTransfer", "auth-collections": "authCollections", "service-tokens": "serviceTokens", users: "users", storage: "storage", "backup-storage": "backupStorage" };
  const key = keys[route.kind];
  if (key && backend.capabilities?.[key] === false) return false;
  if (route.kind === "administrators") return backend.administrators !== undefined;
  if (route.kind === "settings") return backend.settings !== undefined;
  if (route.kind === "schema-transfer") return !!(backend.schema.exportDocument && backend.schema.previewImport && backend.schema.applyImport);
  return true;
}
