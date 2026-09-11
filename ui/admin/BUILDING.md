# Admin bundle provenance

`dist/` is a checked-in production artifact. Its only supported source is the
locked dependency graph and the TypeScript source in this directory.

Run `npm ci` followed by `npm run verify:dist` from `ui/admin`. The command
rebuilds the bundle and fails if that rebuild changes a tracked asset or leaves
an untracked generated asset. Release automation should use the same command.
