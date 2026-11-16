# Runtime image follow-up audit — 2026-11-16

## Committed changes

- `63f7de9e9b7ce03ee9be0e88a834b9981cb47507`: isolated, hash-pinned Debian CA/tzdata payload stage. The script verifies package name/version/architecture, extracts data, generates the CA bundle using Debian's packaged helper with all paths isolated, and exports matching package metadata/copyright. It does not import build-host trust or modify PostgreSQL native libraries.
- `0a5298b3a1e3623360719ce3ca0f1f87d4e0ff81`: sets final-image `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt` so libpq `sslrootcert=system` uses the runtime bundle. The exact copied OpenSSL library otherwise defaults to absent `/usr/lib/ssl` paths. Explicit operator overrides remain possible.

## Signed provenance and exact local verification

`provenance.json` contains package hashes, signed-index digest, Debian archive key fingerprints, pinned OCI manifests, bundle comparison, timezone comparison and test exit statuses. `InRelease`, `Packages.xz` and `signature-verification.txt` retain the signed provenance chain; both package payloads were downloaded from the official Debian security repository and verified against it.

`generate.log`: the exact committed script completed successfully in an isolated output directory. Shell syntax and git diff checks passed. No host packages were installed or replaced.

The probe source is preserved as `check-runtime-data.go.txt` so evidence does not add a Go package to the repository build; copy it to a temporary `.go` file to reproduce it.

`go-old-base.log` and `go-updated-data.log`: a Go 1.26.11 CGO-disabled program used `x509.SystemCertPool` and `time.LoadLocation` with isolated trust/zone paths. Original pinned distroless data failed the intended 148-root count and five timezone offsets; updated data passed. UTC control stayed unchanged. These files do not claim that the original final image used the original distroless CA bundle: its old Dockerfile overwrote that bundle from the Go builder, whose bytes were not retrieved.

`certificate-comparison.json`: original pinned distroless bundle has 150 roots; new bundle has 148. Only AAA Certificate Services and emSign ECC Root CA - G3 were removed, with no additions. New bundle SHA256 is `e9c7b63ee2623386e9b0a3ee32f499d01a8ea23dc83b8383ddff9f8e749543b7`. The complete amd64 distroless layer audit found only a bundle, not individual CA files or symlinks, so overlay cannot retain those removed individual roots.

`timezone-comparison.json`: at 2026-11-16T12:00:00Z, Winnipeg -06→-05, Edmonton/Yellowknife -07→-06, Havana -05→-04, Casablanca +01→+00. All old base timezone file/symlink paths exist in the new overlay. Updated package is 2026f-0+deb12u1; zones are loaded by `internal/jobs/service.go` for schedule parsing. Scheduling in those regions therefore changes to the corrected rules; no API or DB schema changes.

`openssl-trust-path-probe.log`: exact official signed libssl3 3.0.22-1~deb12u2 libcrypto confirms default paths `/usr/lib/ssl/cert.pem` and `/usr/lib/ssl/certs`. `X509_STORE_set_default_paths` loads zero roots for absent paths and all 148 for explicit generated bundle. Source review confirms PostgreSQL extraction adds only ELF closure, docs/status and nsswitch.conf, not those missing trust aliases.

## Original image inventory remains explicit

`image-inventory-review.json` preserves all 37 original Trivy entries with package, severity, fixed version and bounded reachability decision: 21 MEDIUM, 9 LOW, 7 UNKNOWN. The high/critical-only zero report was not a clean all-severity inventory.

Four OpenSSL reports (CVE-2026-88822, -88823, -95691, -95692) have a primary-source applicability conflict: upstream's 2026-11-10 advisory explicitly excludes OpenSSL 3.0, while Debian flags 3.0.22. No allowlist/suppression was added. CMS CVE-2026-107496 affects the library but upstream says TLS/QUIC do not invoke the affected CMS function; no CMS path was found in reviewed PgBase/libpq paths. PowerPC/POWER-specific entries are not applicable to this amd64 artifact, without making claims for other architectures.

The remaining libc/OpenSSL inventory is retained as native-helper dependency risk. Main server is CGO-disabled, but pg_dump/pg_restore carry and use native libraries; the absence of every flagged function path has not been proven. No newer fixed Bookworm libc/OpenSSL package was supplied in the scan. The separate x/crypto update is owned by Performance agent; old OpenPGP packages were not linked before the update, which is dependency hygiene rather than a demonstrated reachable vulnerability fix.

## Limits and next gate

Local Docker was unavailable. No final image build, image scan, TLS integration or browser E2E is claimed here. Root owns the final container build, extraction/inventory, complete source CodeQL and E2E CI, which are the definitive checks for the committed image changes. Removed public CA roots can intentionally stop trusting servers that still chain solely to them; operators need valid replacement chains. Version/hash pin updates should repeat signed metadata and actual data verification.
