import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { getAuthAdministration, getAuthRecord, listAuthCollections, listAuthRecords, listOAuthProviderCatalog, listTokenPolicies, type AuthCollection, type AuthRecord } from "../api/authCollections";
import { ApiError } from "../api/http";
import { listUserOAuthIdentities, listUsers, type ManagedUser } from "../api/users";
import { matchesUser, uuidBefore } from "./forms";

export const authCollectionsQuery = queryOptions({
  queryKey: ["auth", "collections"],
  queryFn: ({ signal }) => listAuthCollections(signal),
  select: (items: AuthCollection[]) => [...items].sort((a, b) => a.name.localeCompare(b.name)),
});

export const administrationQuery = (id: string) =>
  queryOptions({ queryKey: ["auth", "administration", id], queryFn: ({ signal }) => getAuthAdministration(id, signal) });

export const tokenPoliciesQuery = (id: string) =>
  queryOptions({ queryKey: ["auth", "tokens", id], queryFn: ({ signal }) => listTokenPolicies(id, signal) });

export const providerCatalogQuery = queryOptions({
  queryKey: ["auth", "provider-catalog"],
  queryFn: ({ signal }) => listOAuthProviderCatalog(signal),
  staleTime: Infinity,
});

export const usersQuery = infiniteQueryOptions({
  queryKey: ["auth", "users"],
  queryFn: ({ pageParam, signal }) => listUsers({ after: pageParam, limit: 50, signal }),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (page) => page.nextAfter,
});

/** One user by id, for a ?user= link beyond the loaded pages (the API has no single-user read). */
export const userQuery = (id: string) =>
  queryOptions({
    queryKey: ["auth", "users", "one", id],
    queryFn: async ({ signal }) => {
      const after = uuidBefore(id);
      if (after === null) return null;
      const page = await listUsers({ after, limit: 1, signal });
      return page.items.find((user) => user.id === id) ?? null;
    },
  });

export const identitiesQuery = (userId: string) =>
  queryOptions({ queryKey: ["auth", "identities", userId], queryFn: ({ signal }) => listUserOAuthIdentities(userId, signal) });

const missing = (error: unknown) => error instanceof ApiError && (error.status === 400 || error.status === 404);

/** The auth record behind a managed user, with its collection; null when it cannot be found. */
export const userRecordQuery = (user: ManagedUser, collections: AuthCollection[]) =>
  queryOptions({
    queryKey: ["auth", "user-record", user.id],
    queryFn: async ({ signal }): Promise<{ collection: AuthCollection; record: AuthRecord } | null> => {
      // shortcut: the record list has no cursor, so matching covers each collection's first 100 records.
      for (const collection of collections) {
        const record = (await listAuthRecords(collection.id, { limit: 100, signal })).find((candidate) => matchesUser(candidate, user));
        if (record) return { collection, record };
      }
      // Beyond that, uuid-policy collections expose the user UUID as the record id; a miss is a 400/404.
      for (const collection of collections.filter((candidate) => candidate.recordId?.kind !== "text")) {
        try {
          return { collection, record: await getAuthRecord(collection.id, user.id, signal) };
        } catch (error) {
          if (!missing(error)) throw error;
        }
      }
      return null;
    },
  });
