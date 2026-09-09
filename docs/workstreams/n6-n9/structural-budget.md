# N6–N9 measured structural budget

Source candidate: `94d8300`; baseline: merged N1–N5 `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`. N6 mechanical cleanup `c4ebdeb` is included; actual CLI/JSVM/public-facade race tests pass. Gauss independently confirmed the unchanged classifier, baseline metrics and scoped rationale at `d4fb6a2`; the final mechanical delta adds exactly 17 Go lines.

`go run ./tools/repostats -compare 2939f1543e4889c0ffa967eae398c91d0ecbb6ce` measures every Git-tracked runtime file using the existing classifier. No files or comments were excluded and no unused growth allowance is added. The data-group ceiling remains 22,693; no database/query/record/schema runtime file changes in this feature group. Every production file remains at or below 800 lines.

| Metric | N1–N5 baseline | N6–N9 measured ceiling |
| --- | ---: | ---: |
| Go runtime lines | 58,513 | 69,309 |
| UI runtime lines | 6,249 | 6,667 |
| Handwritten runtime lines | 64,762 | 75,976 |
| Go interfaces | 60 | 67 |
| Go exported declarations | 1,053 | 1,251 |
| Go constructors | 49 | 55 |
| UI direct page state/effect calls | 131 | 154 |
| UI runtime exports | 203 | 229 |
| Production files over 800 lines | 0 | 0 |

Gross tracked runtime comparison is +11,258/−48 = +11,210. Four new migration embedding lines are classified as support in comparison, but counted as Go runtime in the metric, accounting for handwritten +11,214. Generated bundles, test files, docs, and Dart SDK packaging are reported separately by the unchanged classifier.

| Runtime area | Added | Removed | Contract served |
| --- | ---: | ---: | --- |
| `internal/jsvm` | 1,238 | 0 | bounded JS engine, loader, cancellation and queue lifecycle |
| `pgbase` | 2,579 | 14 | typed JS domain facade/hooks/jobs/migrations and optional SQL console composition |
| `internal/sqlconsole` | 3,630 | 0 | parser/native capability guard, restricted pools, executor, durable admission/recovery and heavy-data cleanup |
| `internal/selfupdate` | 2,034 | 0 | signed local artifact verification, journaled swap/recovery and platform helpers |
| `internal/cli` | 747 | 4 | JS migration/watch and local self-update command surfaces |
| `internal/api` | 457 | 9 | optional authenticated SQL routes, bounded DTOs and session capability |
| `ui/admin` | 430 | 12 | SQL editor/results/history/recovery, session-scoped pending identity and nav |
| other runtime/support | 147 | 9 | build metadata/config, storage guard, job occurrence identity, PublicDir CSP and external example |

The increased interface/export/constructor counts describe approved independent service boundaries and the public Go facade, rather than wrappers introduced to satisfy a metric. The SQL executor, durable ledger, role inspector, UI recovery state and update journal preserve different trust/lifetime boundaries. Consolidating these into pre-existing packages would mix those boundaries or require unrelated redesign. The exact measured ceilings therefore admit this approved feature scope while retaining future regression limits and the fixed per-file cap.

Migration 41 is the sole new system migration. Its current checksum is `628b3920dd97db4b419ba7f2ed4b3b921911a07d374033ad8e2f0a849871d699`. Migrations 1–40 are byte-identical to the merged baseline; only the new41 checksum snapshot was refreshed during unpublished local development.
