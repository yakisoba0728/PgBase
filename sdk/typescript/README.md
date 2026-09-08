# PgBase TypeScript SDK

Use an explicit auth realm for public users:

```ts
const members = client.realm("members");
const session = await members.register("person@example.com", "password");
await members.updateProfile({ displayName: "Person" });
const { authorizationUrl } = await members.oauthStart("github", "http://localhost:5173/callback");
location.assign(authorizationUrl);
```

`members.login` raises `MFAChallengeError` when a challenge must be completed. Tokens live only in the supplied `AuthStore`; pass `browserTokenStorage(localStorage)` only when persistent browser storage is intended.

## Administrator schema and operations client

Use `PgBaseAdminClient` in a same-origin browser administrator tool. The browser keeps the session cookie; the client holds the CSRF token only in memory. It is separate from the public-user `PgBaseClient`.

```ts
import { PgBaseAdminClient } from "@pgbase/sdk";
const admin = new PgBaseAdminClient(window.location.origin);
await admin.resume(); // or login(email, password)
const catalog = await admin.catalog();
const collection = catalog.collections.posts;
const change = {
  operation: "update" as const,
  name: collection.name,
  fields: collection.fields.map(({ id, name, type, required, unique, indexed, options, default: defaultValue, relation }) => ({
    id, name: name === "title" ? "headline" : name, type, required, unique, indexed, options,
    default: defaultValue,
    relation: relation ? {
      targetCollection: Object.values(catalog.collections).find(item => item.id === relation.targetCollectionId)!.name,
      cardinality: relation.cardinality, minSelect: relation.minSelect, maxSelect: relation.maxSelect, onDelete: relation.onDelete,
    } : undefined,
  })),
};
const planned = await admin.plan(change);
console.log(planned.preview.sql); // inspect the exact preview before applying
const result = await admin.apply(planned);
// Retry the same planned object if the response was lost; it reuses operation IDs.
console.log(result, await admin.operation(planned.operationID));
```

Existing field IDs preserve data when a field is renamed. Omitted options preserve the current constraints; `{}` clears them. `FieldOptions` covers text length/pattern/email/URL/domains/choices, numeric decimal bounds and JSONB multi-select counts. Required follows SQL NULL semantics. The client also exposes `logs`, `jobs`, `configureJob` and `queueJob`.
