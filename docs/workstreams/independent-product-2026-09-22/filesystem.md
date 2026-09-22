# Filesystem facade: explicit native storage contract

Date: 2026-09-22. Scope: the JavaScript filesystem facade, its public declarations, and filesystem integration tests. The PocketBase comparison audit remains an immutable observation of the earlier state.

## Design recorded before implementation

The product contract is operator-selected, namespace-scoped storage. A script must not silently request another bucket or endpoint and write to the current backend instead. Implementing arbitrary S3 clients merely to match another product would expand storage and network authority, so this change explicitly rejects storage configuration arguments.

The initial design proposed rejecting every `s3` call. Coordination with the root task narrowed this to preserve working no-argument calls: `$filesystem.s3()` remains a deprecated alias for the new `$filesystem.configured()`. Both use the existing capability host and operator-selected local/S3 backend. Any supplied argument, including an explicit `undefined`, is rejected; no credentials or endpoints are copied into diagnostics.

The agreed test seam is an actual admitted JavaScript HTTP route calling the filesystem service, backed by a fresh PostgreSQL test database and local or configured MinIO storage. This seam was selected by the assigned native-correctness task before writing the regression tests. The TDD skill was applied at that authorized seam; no additional approval was needed.

## Resulting contract

- `configured()` takes no arguments. `s3()` is its deprecated no-argument alias. Arguments cause `filesystem_configuration_unsupported` before storage access.
- Configured systems expose `listPage(prefix = "", cursor = "", limit = 100)`. The limit is an integer from 1 to 1000. The result is `{objects, nextCursor}`; the existing bridge metadata uses `{Key, Size, UpdatedAt}`. A nonempty cursor can be passed to the next call with the same prefix. Limits outside this range cause `filesystem_invalid_page_limit`.
- Configured `list(prefix)` remains an array convenience when the complete result fits in 100 objects. A further page causes `filesystem_list_requires_pagination` instead of a success response containing only the first page. The existing top-level `$filesystem.list` bridge shape is unchanged.
- Both configured and rooted local systems reject `setContext()` with `filesystem_context_unsupported`. Cancellation/deadlines continue to come from the invocation. The script cannot replace or lengthen them.
- Capability checks, module namespace, object-key validation, operator roots, transaction behavior, and stream limits stay in the existing host/service path. No new storage client, bridge privilege, database schema, or external storage target was introduced.

The public JavaScript guide and both copies of the generated declaration contract describe these choices. Existing S3 serving tests now select `configured()` rather than passing dummy arguments that used to be silently ignored.

## Red → green evidence

Tests live in `pgbase/javascript_filesystem_contract_integration_test.go` and exercise actual routes/services, not mocked storage.

| Regression | Observed red result | Green behavior |
| --- | --- | --- |
| `TestJavaScriptFilesystemRejectsStorageConfiguration` | Requesting another bucket/endpoint returned `{"code":"accepted","objects":1}`: it wrote to the configured store. | `filesystem_configuration_unsupported`, no unexpected object; a native write is visible through the preserved no-argument alias. |
| `TestJavaScriptFilesystemListingRequiresContinuation` | Creating 101 matching objects then calling `list()` returned `{"code":"accepted"}`. | Explicit pagination error; pages contain 100 then 1 object, cursor `items/099`, final key `items/100`, empty terminal cursor, and another prefix is excluded. Small complete lists still work. Invalid limits 0, 1001 and 1.5 are rejected. |
| `TestJavaScriptFilesystemCannotReplaceInvocationContext` | Both configured and local systems returned `{"codes":["accepted","accepted"]}`. | Both return `filesystem_context_unsupported` when asked to replace the context. |

Each regression was run and failed before the associated production change. The first test's object count was then adjusted to use the existing facade's object-entry filtering when inspecting the raw bridge page; its original storage-configuration red reproduced a real write. A separate existing configured-S3 system test verifies upload, read/seek, copy, delete, and empty-prefix behavior on MinIO.

## Executed verification

The three targeted regression commands used this form, with each test selected by name before its fix and again after it:

```sh
docker exec -w /workspace pgbase-remaining-runner \
  go test -tags=integration ./pgbase \
  -run '^TestJavaScriptFilesystemRejectsStorageConfiguration$' -count=1
```

Final filesystem integration selection passed through the repository's no-skip guard:

```sh
docker exec \
  -e PGBASE_TEST_S3_ENDPOINT=http://127.0.0.1:59039 \
  -e PGBASE_INTEGRATION_PACKAGES=./pgbase \
  -w /workspace pgbase-remaining-runner \
  bash scripts/test-go-integration.sh \
  -run 'Test(JavaScript(Filesystem.*|ConfiguredS3FilesystemSystemMethods|LocalSystemServesRootedFile|SystemServe.*)|PocketBase(Filesystem.*|LocalFilesystemSystemMethods)|ExtensionObjectsStreamListCopy.*)' \
  -count=1
```

Result: `PASS`, package `pgbase` passed in 7.292s. This includes native listing/configuration/context regressions, local/S3 object streams, system serving and thumbnails, invocation-bound response handles, and rooted local serving restrictions. The existing serving tests initially failed because their dummy-argument constructor calls now correctly reject; only their storage selection was updated before rerunning.

```sh
docker exec -w /workspace pgbase-remaining-runner \
  go test ./internal/jsvm ./pgbase \
  -run 'Test(PocketBase.*|JavaScriptTypeDeclaration.*)' -count=1
```

Result: both packages passed, including the generated declaration artifact synchronization check. `gofmt` and `git diff --check` passed for the changed files.

The runner's original `http://minio:9000` endpoint failed the existing insecure-loopback policy before application startup. The root task provided its existing runner-local proxy at `127.0.0.1:59039`; rerunning with that override passed. This subtask did not start/restart Docker services or modify storage policy to bypass the restriction. The root task owns proxy cleanup.

## Boundaries

This work makes the native API honest; it does not add arbitrary per-script S3 credentials, a mutable Go context, or PocketBase API parity. The list cursor retains the existing live ordered-key semantics and is not a snapshot across concurrent object changes. Invocation host-call/time/output limits still apply to traversal of many pages. The broader application test suite and release validation are owned by the root task; this report claims only the checks listed above.
