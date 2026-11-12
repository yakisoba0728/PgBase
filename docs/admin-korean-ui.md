# Korean administrator UI and visual refinement

Started 2026-11-12 from deployed baseline `f7de2f5` on
`codex/korean-admin-polish`. The earlier claim that the complete UI/UX was
finished described functional coverage, not visual acceptance. This pass adds
Korean support and refines the actual PocketBase-inspired workbench.

## Language behavior

- Korean and English are available before and after authentication. The first
  visit follows browser language; selection persists on the current device.
- Explicit React translations cover navigation, forms, help, options, errors,
  confirmation dialogs and accessible names. No DOM text replacement is used.
- Canonical bilingual feedback updates when switching language. Routes, forms,
  passwords, pending operations, restore locks and unsaved drafts stay mounted.
- Another tab's language selection synchronizes without discarding a draft.
- User identifiers, field names, SQL, JSON, mail-template content and exact
  stored/exported numbers remain unchanged. Unknown backend diagnostics and
  extension-owned content retain their original text.
- Only built-in creation/update columns use compact locale-aware dates;
  exact RFC3339 values remain in titles and `datetime` attributes. Custom
  timestamp values and edit/export formats remain unchanged.

## Visual changes

The normal collection sidebar contains the collection list, creation action
and settings entry, instead of duplicate global groups. Settings exposes the
remaining tools; a capability-aware fallback keeps supported children visible
when a parent is unavailable. The records page removes redundant context tabs,
uses a compact search/filter bar and quiet secondary actions, puts user fields
before timestamps, and constrains UUID/time display widths. Korean typography,
language pickers, monochrome selection, contrast, light/dark and mobile layouts
share the existing stylesheet. No runtime dependency was added.

## Verification

- Node 24.21.0: 64 files / 742 UI tests passed, typecheck and production build
  passed. The complete unit run uses six workers to avoid oversubscribing the
  developer machine; test deadlines and assertions are unchanged.
- Locale tests include existing-error translation, draft/password/OTP state,
  restore locks, storage failure, cross-tab updates, exact numbers and raw API
  data. Existing English tests explicitly use English.
- Structural measurement: Go 94,993 + UI 14,643 = 109,636 runtime lines;
  +2815/-1122/net +1693 against baseline, 233 page hooks, 375 UI exports,
  no runtime files above 800 lines. Classifiers are unchanged.
- Browser evidence is recorded after the final metadata build in the ignored
  local directory `.local/korean-ui-evidence/`.
- Actual final browser coverage: 31 authenticated scenarios passed (29 English,
  two Korean), plus onboarding passed; four existing managed/MinIO opt-ins
  remain skipped. The complete 35-case run passed 30 and skipped four; one
  remaining table selector also matched the API reference table. Scoping that
  assertion to the record-table region preserved its sorting check, and its
  focused rerun passed. No product retry or test assertion was removed.
- Korean screenshots verify the compact final metadata display on desktop,
  mobile and dark theme. Runtime locale switching preserves login/record
  errors, drafts and original timestamp attributes. Tests use isolated
  headless Chromium, never the user's Chrome profile or Computer Use.

## Deployment boundary

The baseline was deployed first to `https://pgbase.yaki.kr/admin/` with its
database backed up. Subsequent deployment and monitoring use the Dokploy API;
Computer Use was stopped and task-created Chrome tabs were closed at the
user's request. API credentials are kept outside Git and never printed in
verification logs. See the deployment record for exact revisions and results.

The Korean UI update was deployed through API from `c1e6ef3` and verified by
public health responses and exact served-asset SHA256 matches. The source
deployment is recorded in [the deployment log](deployment-2026-11-12.md).
