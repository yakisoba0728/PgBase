import { lazy, type ComponentType } from "react";
import { createBrowserRouter, Navigate } from "react-router";
import { LoginScreen } from "../guest/LoginScreen";
import { ResetPasswordScreen } from "../guest/ResetPasswordScreen";
import { SetupScreen } from "../guest/SetupScreen";
import { NotFoundPage } from "../pages/NotFoundPage";
import { AppShell } from "./AppShell";
import { ExtensionScreenPage } from "./extensions";
import { RequireAuth } from "./guard";
import { routeModules } from "./routeModules";

// Each screen is its own chunk, loaded on first visit; the shell's Suspense shows the loader meanwhile.
const page = <M,>(load: () => Promise<M>, name: keyof M) => lazy(() => load().then((module) => ({ default: module[name] as ComponentType })));

const CollectionPage = page(routeModules["/collections"], "CollectionPage");
const CollectionsIndexPage = page(routeModules["/collections"], "CollectionsIndexPage");
const NewCollectionPage = page(routeModules["/new-collection"], "NewCollectionPage");
const LogsPage = page(routeModules["/logs"], "LogsPage");
const JobsPage = page(routeModules["/jobs"], "JobsPage");
const RealtimePage = page(routeModules["/realtime"], "RealtimePage");
const AuthCollectionsPage = page(routeModules["/auth/collections"], "AuthCollectionsPage");
const AuthUsersPage = page(routeModules["/auth/users"], "AuthUsersPage");
const BackupsPage = page(routeModules["/backups"], "BackupsPage");
const StoragePage = page(routeModules["/storage"], "StoragePage");
const SettingsPage = page(routeModules["/settings"], "SettingsPage");
const ServiceTokensPage = page(routeModules["/service-tokens"], "ServiceTokensPage");
const AdministratorsPage = page(routeModules["/administrators"], "AdministratorsPage");
const ProfilePage = page(routeModules["/profile"], "ProfilePage");
const SchemaTransferPage = page(routeModules["/schema-transfer"], "SchemaTransferPage");
const SchemaOperationsPage = page(routeModules["/schema-operations"], "SchemaOperationsPage");
const SqlConsolePage = page(routeModules["/sql"], "SqlConsolePage");

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
