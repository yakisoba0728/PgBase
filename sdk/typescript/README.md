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
