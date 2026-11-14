export const routeModules = {
  "/collections": () => import("../pages/CollectionPage"),
  "/new-collection": () => import("../pages/NewCollectionPage"),
  "/logs": () => import("../pages/LogsPage"),
  "/jobs": () => import("../pages/JobsPage"),
  "/realtime": () => import("../pages/RealtimePage"),
  "/auth/collections": () => import("../pages/AuthCollectionsPage"),
  "/auth/users": () => import("../pages/AuthUsersPage"),
  "/backups": () => import("../pages/BackupsPage"),
  "/storage": () => import("../pages/StoragePage"),
  "/settings": () => import("../pages/SettingsPage"),
  "/service-tokens": () => import("../pages/ServiceTokensPage"),
  "/administrators": () => import("../pages/AdministratorsPage"),
  "/profile": () => import("../pages/ProfilePage"),
  "/schema-transfer": () => import("../pages/SchemaTransferPage"),
  "/schema-operations": () => import("../pages/SchemaOperationsPage"),
  "/sql": () => import("../pages/SqlConsolePage"),
};

/** Load only screen code on intent; its API queries still start on navigation. */
export async function preloadRoute(path: string): Promise<void> {
  const pathname = path.split(/[?#]/, 1)[0];
  const prefix = (Object.keys(routeModules) as (keyof typeof routeModules)[])
    .sort((a, b) => b.length - a.length)
    .find((prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`));
  if (prefix) {
    // A failed speculative load must not prevent the actual navigation from retrying.
    await routeModules[prefix]().catch(() => {});
  }
}
