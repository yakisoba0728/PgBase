import { createBrowserRouter, Navigate, type RouterNavigateOptions, type To } from "react-router";
import { LoginScreen } from "../guest/LoginScreen";
import { ResetPasswordScreen } from "../guest/ResetPasswordScreen";
import { SetupScreen } from "../guest/SetupScreen";
import { NotFoundPage } from "../pages/NotFoundPage";
import { AppShell } from "./AppShell";
import { ExtensionScreenPage } from "./extensions";
import { RequireAuth } from "./guard";
import { screen } from "./screens";
import { markTransition, navigationTransition, routeTransition } from "./transitions";

// Each screen is its own chunk, loaded on first visit (or ahead, see screens.ts); the shell's Suspense shows the loader meanwhile.
const CollectionPage = screen("collections", () => import("../pages/CollectionPage"), "CollectionPage");
const CollectionsIndexPage = screen("collections", () => import("../pages/CollectionPage"), "CollectionsIndexPage");
const NewCollectionPage = screen("new-collection", () => import("../pages/NewCollectionPage"), "NewCollectionPage");
const LogsPage = screen("logs", () => import("../pages/LogsPage"), "LogsPage");
const JobsPage = screen("jobs", () => import("../pages/JobsPage"), "JobsPage");
const RealtimePage = screen("realtime", () => import("../pages/RealtimePage"), "RealtimePage");
const AuthCollectionsPage = screen("auth/collections", () => import("../pages/AuthCollectionsPage"), "AuthCollectionsPage");
const AuthUsersPage = screen("auth/users", () => import("../pages/AuthUsersPage"), "AuthUsersPage");
const BackupsPage = screen("backups", () => import("../pages/BackupsPage"), "BackupsPage");
const StoragePage = screen("storage", () => import("../pages/StoragePage"), "StoragePage");
const SettingsPage = screen("settings", () => import("../pages/SettingsPage"), "SettingsPage");
const ServiceTokensPage = screen("service-tokens", () => import("../pages/ServiceTokensPage"), "ServiceTokensPage");
const AdministratorsPage = screen("administrators", () => import("../pages/AdministratorsPage"), "AdministratorsPage");
const ProfilePage = screen("profile", () => import("../pages/ProfilePage"), "ProfilePage");
const SchemaTransferPage = screen("schema-transfer", () => import("../pages/SchemaTransferPage"), "SchemaTransferPage");
const SchemaOperationsPage = screen("schema-operations", () => import("../pages/SchemaOperationsPage"), "SchemaOperationsPage");
const SqlConsolePage = screen("sql", () => import("../pages/SqlConsolePage"), "SqlConsolePage");

export const router = createBrowserRouter(
  [
    { path: "/setup", element: <SetupScreen /> },
    { path: "/login", element: <LoginScreen /> },
    { path: "/reset-password", element: <ResetPasswordScreen /> },
    {
      element: (
        <RequireAuth>
          <AppShell />
        </RequireAuth>
      ),
      children: [
        { index: true, element: <Navigate to="/collections" replace /> },
        { path: "collections", element: <CollectionsIndexPage /> },
        { path: "collections/:name/:tab?", element: <CollectionPage /> },
        { path: "new-collection/:type?", element: <NewCollectionPage /> },
        { path: "logs", element: <LogsPage /> },
        { path: "jobs", element: <JobsPage /> },
        { path: "realtime", element: <RealtimePage /> },
        { path: "auth/collections/:name?/:tab?", element: <AuthCollectionsPage /> },
        { path: "auth/users", element: <AuthUsersPage /> },
        { path: "backups", element: <BackupsPage /> },
        { path: "storage/:tab?", element: <StoragePage /> },
        { path: "settings/:tab?", element: <SettingsPage /> },
        { path: "service-tokens", element: <ServiceTokensPage /> },
        { path: "administrators", element: <AdministratorsPage /> },
        { path: "profile", element: <ProfilePage /> },
        { path: "schema-transfer/:tab?", element: <SchemaTransferPage /> },
        { path: "schema-operations", element: <SchemaOperationsPage /> },
        { path: "sql", element: <SqlConsolePage /> },
        { path: "ext/:id/*", element: <ExtensionScreenPage /> },
        { path: "*", element: <NotFoundPage /> },
      ],
    },
  ],
  { basename: "/admin" },
);

// Path changes play a view transition (base.css names what they snapshot); search-only changes (filters, the open
// record) and sibling tabs (Page.tsx slides those) do not. Every link and navigate() call goes through here.
const navigate = router.navigate.bind(router) as (to: To | number | null, options?: RouterNavigateOptions) => Promise<void>;
router.navigate = ((to: To | number | null, options?: RouterNavigateOptions) => {
  if (typeof to === "number" || to === null) return navigate(to, options);
  const kind = navigationTransition(router.state.location.pathname, typeof to === "string" ? to : (to.pathname ?? ""), router.basename ?? "");
  return navigate(to, kind ? { ...options, viewTransition: options?.viewTransition ?? true } : options);
}) as typeof router.navigate;
router.subscribe((_state, { viewTransitionOpts }) => {
  const kind = viewTransitionOpts && routeTransition(viewTransitionOpts.currentLocation.pathname, viewTransitionOpts.nextLocation.pathname, router.basename === "/" ? "" : router.basename);
  if (kind) markTransition(kind);
});
