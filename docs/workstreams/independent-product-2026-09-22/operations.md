# Native API quota labels and upgrade safety

API quota labels describe PgBase's registered route patterns, optionally prefixed by an uppercase HTTP method. A trailing slash matches a pattern prefix. They do not describe individual record URLs or PocketBase collection/action tags.

New settings writes reject ineffective tag syntax, whitespace/control characters, query strings, fragments, and labels over 256 UTF-8 bytes. The administrator UI follows the same syntax and byte limit. Its examples distinguish administrator record lists from user record lists.

The matcher itself is unchanged: method-qualified candidates are considered first, then the bare route pattern; the first applicable rule in the candidate group wins. Buckets include the selected label/audience, authenticated caller identity, and client IP. Syntactically valid labels are not checked against the current route registry, so a typo or an absent extension route can still leave a rule unused.

## Upgrade behavior

Previously accepted encrypted snapshots may contain dormant labels such as `posts:list`. Startup/reload retains the old stored-document validation so these installations remain bootable and their settings remain readable. The labels are neither translated nor given fictional matching behavior. Existing valid rules keep working through the same matcher.

Saving any new settings revision requires repairing or removing invalid labels. A rejected update changes neither published settings nor the encrypted persisted document. After repair, another controller can reload the corrected revision. Existing numeric, audience, duplicate, IP, and document validation still applies to stored snapshots.

This separation matters because `runtimesettings.New` calls `Reload`, and an error there aborts application construction. Applying new syntax restrictions directly to stored snapshots initially broke upgrades.

## Actual verification

The upgrade regression was run before the stored/new-write validation split:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/runtimesettings -run '^TestLegacyRateLabelsReloadButMustBeRepairedBeforeUpdate$' -count=1 -p=2
FAIL: reload legacy encrypted settings must remain repairable: invalid runtime settings (0.105s)
```

The test writes an encrypted legacy snapshot into an isolated migrated database, reloads/restarts controllers, verifies that an unrepaired update is rejected, repairs the label list, and reloads the resulting revision. It preserves a valid route rule alongside the dormant tag to catch unintended configuration replacement.

After the root implementation split `validateStored` from strict update validation:

```text
docker exec -w /workspace pgbase-remaining-runner go test -race -tags=integration ./internal/runtimesettings ./internal/apiratelimit -count=1 -p=2
PASS internal/runtimesettings (1.756s)
PASS internal/apiratelimit (1.149s)

# From ui/admin:
npm.cmd exec -- vitest run src/pages/api-rate-limit-settings.test.ts
PASS: 17 tests, 1 file
```

The backend run also covers `TestInvalidRateLabelCannotReplacePublishedOrPersistedSettings`, encrypted settings/CAS regressions, and the quota package's integration suite. `git diff --check -- internal/runtimesettings` passed. These checks do not claim that arbitrary operator-defined labels match a live endpoint or that all deployment paths have been exercised.
