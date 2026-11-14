import { infiniteQueryOptions, queryOptions, type Query, type QueryClient } from "@tanstack/react-query";
import { pageWindow, windowShifted } from "../app/queries";
import { getAuthAdministration, listAuthCollections, listAuthRecords, listOAuthProviderCatalog, listTokenPolicies, type AuthCollection, type AuthRecord } from "../api/authCollections";
import { ApiError } from "../api/http";
import { getUser, listUserOAuthIdentities, listUsers, type ManagedUser } from "../api/users";
import { matchesUser, uuidBefore } from "./forms";

export const authCollectionsQuery = queryOptions({
  queryKey: ["auth", "collections"],
  queryFn: ({ signal }) => listAuthCollections(signal),
  select: (items: AuthCollection[]) => [...items].sort((a, b) => a.name.localeCompare(b.name)),
});

export const administrationQuery = (id: string) =>
  queryOptions({ queryKey: ["auth", "administration", id], refetchOnReconnect: false, queryFn: ({ signal }) => getAuthAdministration(id, signal) });

export const tokenPoliciesQuery = (id: string) =>
  queryOptions({ queryKey: ["auth", "tokens", id], refetchOnReconnect: false, queryFn: ({ signal }) => listTokenPolicies(id, signal) });

export const providerCatalogQuery = queryOptions({
  queryKey: ["auth", "provider-catalog"],
  queryFn: ({ signal }) => listOAuthProviderCatalog(signal),
  staleTime: Infinity,
});

const missing = (error: unknown) => error instanceof ApiError && (error.status === 400 || error.status === 404);

/** Users by id; the server filters by collection and by an email substring or exact id (`q`). The first page carries the (capped) total. */
export const usersQuery = (filter: { q: string; collection: string }) =>
  infiniteQueryOptions({
    queryKey: ["auth", "users", "list", filter.q, filter.collection],
    queryFn: ({ pageParam, signal }) =>
      listUsers({ after: pageParam, limit: 100, q: filter.q || undefined, authCollectionId: filter.collection || undefined, total: pageParam === undefined, signal }),
    ...pageWindow,
    getNextPageParam: (page) => page.nextAfter,
  });

/**
 * After users were created or deleted. A list whose window moved past its first page has no fresh total to show,
 * so it starts over from that page (as "back to start" does); every other users query just refetches.
 */
export function refreshUserLists(queryClient: QueryClient) {
  // Collected first: once reset, a list no longer looks shifted.
  const restart = new Set<Query>(queryClient.getQueryCache().findAll({ queryKey: ["auth", "users", "list"], predicate: (query) => windowShifted(query.state.data as { pageParams: unknown[] } | undefined) }));
  return Promise.all([
    queryClient.resetQueries({ queryKey: ["auth", "users"], predicate: (query) => restart.has(query) }),
    queryClient.invalidateQueries({ queryKey: ["auth", "users"], predicate: (query) => !restart.has(query) }),
  ]);
}

/** One user by id; null when there is none. */
export const userQuery = (id: string) =>
  queryOptions({
    queryKey: ["auth", "users", "one", id],
    queryFn: async ({ signal }) => {
      try {
        return await getUser(id, signal);
      } catch (error) {
        if (missing(error)) return null;
        throw error;
      }
    },
  });

export const identitiesQuery = (userId: string) =>
  queryOptions({ queryKey: ["auth", "identities", userId], queryFn: ({ signal }) => listUserOAuthIdentities(userId, signal) });

/** The auth record behind a managed user, with its collection; null when it cannot be found. */
export const userRecordQuery = (user: ManagedUser, collections: AuthCollection[]) =>
  queryOptions({
    queryKey: ["auth", "user-record", user.id],
    queryFn: async ({ signal }): Promise<{ collection: AuthCollection; record: AuthRecord } | null> => {
      const collection = collections.find((candidate) => candidate.id === user.authCollectionId);
      if (!collection) return null;
      // Record pages are keyed by the internal user id, so the page that starts just before it opens on this user's record.
      const page = await listAuthRecords(collection.id, { after: uuidBefore(user.id) ?? undefined, limit: 1, signal });
      const record = page.items.find((candidate) => matchesUser(candidate, user));
      return record ? { collection, record } : null;
    },
  });
