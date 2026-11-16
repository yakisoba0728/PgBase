# Candidate security recheck: run 43795108138

This is the security agent's independent review of artifacts from [run 43795108138](https://github.com/yakisoba0728/PgBase/actions/runs/43795108138), not a new locally executed scan. Published source head `9934a687f7f9ee093f45562d2f801dad2dbf5c65`, analyzed merge `69fa5e7707cdad8799ef82999b58a92772c75bca` and local source `1e5e3d39cb890ed448fb4ef076a14822f0eb6122` were independently checked to share tree `0e4d7a35239bab166fabdb881f9ec3f5e12b01e7`.

## Evidence

- `recheck.json`: SHA-256/API-size verification for all five input ZIPs; all 25 source result locations/fingerprints and their review; all 32 image records/dispositions; explicit extraction coverage/exclusions; Git blob comparison of 67 reported-flow files and 22 additional guard files; direct DER/certificate/timezone/package/SBOM checks.
- `job-evidence.json`: GitHub API step results, exact completed CodeQL query evaluations, merge comparison, decoded-log hashes and gzip hashes.
- `source-go-job.log.gz`, `source-js-job.log.gz`, `supply-chain-job.log.gz`: complete decoded job logs fetched independently through the GitHub connector and stored as UTF-8, then gzip-compressed with a deterministic zero modification time. No failing/warning/skipped lines were removed.
- `manifest.json`: hashes and sizes of this directory's artifacts.

The original source/image/SBOM/runtime-assets ZIPs and `artifact-api.json` are in [the integration workstream's attempt-1 directory](../../final-qualification/attempt-1/). Their API digests were compared with the downloaded ZIP bytes before reading their contents. The raw source reports remain unmodified; review decisions are separate fields in this directory.

Go query evaluation completed 37/37 and JS/TS 105/105. CodeQL's full-scope checks passed for configured source without diff-range/incremental restriction. That still means the extracted Linux Go runtime and configured JS/TS, not all operating-system implementations, Go test files or Dart. The image gate judges HIGH/CRITICAL; zero gate results does not remove the 32 MEDIUM/LOW/UNKNOWN records.

The bundle check independently decoded and fingerprinted all 148 certificates. It confirms the captured bytes and absence of the two previously removed roots; it does not certify every trust anchor's validity, rerun TLS handshakes or establish native-library nonreachability. The expired and legacy nonpositive-serial certificate observations are retained in the JSON. No production service or image was changed.

The complete interpretation and remaining boundaries are in [final-security.md](../../../final-security.md). Other full-CI gates failed on this attempt; later test-only fixes need their own qualification evidence. A runtime change requires renewed scanner/applicability assessment.
