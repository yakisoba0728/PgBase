import { createBrowserRouter, Navigate } from "react-router";
import { LoginScreen } from "../guest/LoginScreen";
import { ResetPasswordScreen } from "../guest/ResetPasswordScreen";
import { SetupScreen } from "../guest/SetupScreen";
import { CollectionPage, CollectionsIndexPage } from "../pages/CollectionPage";
import { NewCollectionPage } from "../pages/NewCollectionPage";
import { NotFoundPage, PlaceholderPage } from "../pages/placeholders";
import { AppShell } from "./AppShell";
import { ExtensionScreenPage } from "./extensions";
import { RequireAuth } from "./guard";
import { navGroups } from "./navigation";
import { JobsPage } from "../pages/JobsPage";
import { LogsPage } from "../pages/LogsPage";
import { RealtimePage } from "../pages/RealtimePage";
import { BackupsPage } from "../pages/BackupsPage";
import { StoragePage } from "../pages/StoragePage";

const implemented = new Set<string>(["logs", "jobs", "realtime", "auth/collections", "auth/users", "logs", "jobs", "realtime", "backups", "storage"]);
import { AuthCollectionsPage } from "../pages/AuthCollectionsPage";
import { AuthUsersPage } from "../pages/AuthUsersPage";


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
        ...navGroups.flatMap((group) => group.items.filter((item) => !implemented.has(item.path)).map((item) => ({ path: item.path, element: <PlaceholderPage title={item.label} icon={item.icon} /> }))),
        { path: "ext/:id/*", element: <ExtensionScreenPage /> },
        { path: "*", element: <NotFoundPage /> },
      ],
    },
  ],
  { basename: "/admin" },
);
