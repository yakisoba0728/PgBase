# Private notes example

Build the local SDK, then start the development server:

```sh
npm --prefix sdk/typescript ci
npm --prefix sdk/typescript run build
npm --prefix examples/user-notes run serve
```

Open `http://127.0.0.1:5173` and enter the address of a local PgBase server.
For a fresh server, enable user registration and use an administrator to create
the `notes` collection with a `title` text field and an optional `attachment` file field, then enable its user access setting. `scripts/dev-local.sh` enables registration, local files, mail and the example CORS origin. The example server exposes only its HTML, JavaScript and built SDK; it never serves repository credentials or project data.

The example keeps its bearer token in memory. Reloading the page signs the user
out. Applications that choose browser persistence can construct an `AuthStore`
with `browserTokenStorage(window.localStorage)` explicitly.

After signing in, the page opens the notes SSE stream and refreshes its private
list after a record event. The account section exercises reset requests,
verification, and email-change confirmation. Development email action tokens
are delivered by the configured local mailer rather than displayed by the page.
