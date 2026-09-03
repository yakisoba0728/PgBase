# Repository metrics

`repostats` emits the R0 structural and LOC metrics as JSON. It reads only Git-tracked files.

Measure a committed revision:

```sh
go run ./tools/repostats --rev 0fdc66c2fe930951b1934ad4f7ab20288109bf22 --pretty
```

Measure the tracked working tree and include gross additions, deletions, and net changes from a base revision:

```sh
go run ./tools/repostats --compare 0fdc66c2fe930951b1934ad4f7ab20288109bf22 --pretty
```

The comparison categories are disjoint: `runtime`, `test`, `support`, `generated`, `docs`, and `config`. They use the same runtime classification as the primary metrics, including `internal/testutil`, so category net LOC reconciles with two measurements. Playwright `*.spec.ts(x)` files are tests, not UI runtime.

LOC means newline bytes, matching `wc -l`, including for binary blobs and files without a final newline. Git's textual numstat is reconciled to that definition per changed file. A binary content change with no newline-count change therefore has zero gross LOC even though it still increments `files_changed`. Exact renames are deliberately represented as one deletion and one addition (`--no-renames`) so cross-category movement stays visible.

Untracked files are intentionally absent. Add a new file to the Git index before expecting it in a working-tree report. Sparse-checkout files marked `skip-worktree` are read from the index and remain part of the tracked measurement.

The checked-in [R0 baseline](testdata/r0-baseline-0fdc66c2.json) records the authoritative plan digest, the reproducible values, and definition conflicts found during measurement. In particular, the plan's UI comment value `42` is a legacy all-source regex result. The actual UI runtime contains 30 comment-only/comment-like lines; the legacy result includes 11 Vitest comment lines and misclassifies the CSS universal selector as a comment.
