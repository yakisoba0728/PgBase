import { lazy, type ComponentType } from "react";
import { createBrowserRouter, Navigate } from "react-router";
import { LoginScreen } from "../guest/LoginScreen";
import { ResetPasswordScreen } from "../guest/ResetPasswordScreen";
import { SetupScreen } from "../guest/SetupScreen";
import { NotFoundPage } from "../pages/NotFoundPage";
import { AppShell } from "./AppShell";
import { ExtensionScreenPage } from "./extensions";
import { RequireAuth } from "./guard";

// Each screen is its own chunk, loaded on first visit; the shell's Suspense shows the loader meanwhile.
const page = <M,>(load: () => Promise<M>, name: keyof M) => lazy(() => load().then((module) => ({ default: module[name] as ComponentType })));

const CollectionPage = page(() => import("../pages/CollectionPage"), "CollectionPage");
const CollectionsIndexPage = page(() => import("../pages/CollectionPage"), "CollectionsIndexPage");
const NewCollectionPage = page(() => import("../pages/NewCollectionPage"), "NewCollectionPage");
const LogsPage = page(() => import("../pages/LogsPage"), "LogsPage");
const JobsPage = page(() => import("../pages/JobsPage"), "JobsPage");
const RealtimePage = page(() => import("../pages/RealtimePage"), "RealtimePage");
const AuthCollectionsPage = page(() => import("../pages/AuthCollectionsPage"), "AuthCollectionsPage");
const AuthUsersPage = page(() => import("../pages/AuthUsersPage"), "AuthUsersPage");
const BackupsPage = page(() => import("../pages/BackupsPage"), "BackupsPage");
const StoragePage = page(() => import("../pages/StoragePage"), "StoragePage");
const SettingsPage = page(() => import("../pages/SettingsPage"), "SettingsPage");
const ServiceTokensPage = page(() => import("../pages/ServiceTokensPage"), "ServiceTokensPage");
const AdministratorsPage = page(() => import("../pages/AdministratorsPage"), "AdministratorsPage");
const ProfilePage = page(() => import("../pages/ProfilePage"), "ProfilePage");
const SchemaTransferPage = page(() => import("../pages/SchemaTransferPage"), "SchemaTransferPage");
const SchemaOperationsPage = page(() => import("../pages/SchemaOperationsPage"), "SchemaOperationsPage");
const SqlConsolePage = page(() => import("../pages/SqlConsolePage"), "SqlConsolePage");

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
