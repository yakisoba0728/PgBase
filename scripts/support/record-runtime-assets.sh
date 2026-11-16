#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
  echo "usage: record-runtime-assets.sh IMAGE OUTPUT_DIRECTORY" >&2
  exit 2
fi

image_ref=$1
output_directory=$2
# Refuse an existing directory so prior captures cannot contaminate evidence.
mkdir -- "$output_directory"
container_id=$(docker create "$image_ref")
trap 'docker rm -f "$container_id" >/dev/null 2>&1 || true' EXIT

# Inspect the final filesystem without starting the application or a shell.
# These paths contain public trust/timezone data and package metadata only.
docker image inspect --format '{{.Id}}' "$image_ref" > "$output_directory/image-id.txt"
docker image inspect --format '{{json .Config.Env}}' "$image_ref" \
  | python3 -c 'import json,sys; print(json.dumps({k:v for entry in json.load(sys.stdin) or [] for k,_,v in [entry.partition("=")] if k in ("SSL_CERT_FILE", "SSL_CERT_DIR")}))' \
  > "$output_directory/trust-environment.json"
docker cp "$container_id:/etc/ssl/certs" "$output_directory/certs"
docker cp "$container_id:/usr/share/zoneinfo/tzdata.zi" "$output_directory/tzdata.zi"
docker cp "$container_id:/var/lib/dpkg/status.d" "$output_directory/package-status"

python3 - "$output_directory" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys

root = Path(sys.argv[1])
certificates = {}
files = {}
symlinks = {}
for path in sorted((root / "certs").rglob("*")):
    name = path.relative_to(root).as_posix()
    if path.is_symlink():
        # Do not follow a container symlink into the runner's filesystem.
        symlinks[name] = os.readlink(path)
        continue
    if not path.is_file():
        continue
    raw = path.read_bytes()
    files[name] = hashlib.sha256(raw).hexdigest()
    for block in re.findall(br"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", raw, re.S):
        der = ssl.PEM_cert_to_DER_cert(block.decode("ascii"))
        fingerprint = hashlib.sha256(der).hexdigest()
        if fingerprint not in certificates:
            detail = subprocess.run(
                ["openssl", "x509", "-noout", "-subject", "-issuer", "-dates"],
                input=block, capture_output=True, check=True,
            ).stdout.decode("utf-8")
            certificates[fingerprint] = {"description": detail.splitlines(), "files": []}
        certificates[fingerprint]["files"].append(name)
if not certificates:
    raise SystemExit("Final runtime trust directory has no readable PEM certificates")

packages = {}
for path in sorted((root / "package-status").rglob("*")):
    if path.is_symlink() or not path.is_file():
        continue
    text = path.read_text(errors="replace")
    for stanza in text.split("\n\n"):
        package = re.search(r"^Package: (.+)$", stanza, re.M)
        version = re.search(r"^Version: (.+)$", stanza, re.M)
        if package and version:
            packages[package[1]] = version[1]
timezone = (root / "tzdata.zi").read_bytes()
summary = {
    "imageID": (root / "image-id.txt").read_text().strip(),
    "trustEnvironment": json.loads((root / "trust-environment.json").read_text()),
    "certificateCount": len(certificates),
    "certificatesByDER_SHA256": certificates,
    "certificateFileSHA256": files,
    "certificateSymlinks": symlinks,
    "timezoneVersionLine": timezone.decode("utf-8").splitlines()[0],
    "timezoneSourceSHA256": hashlib.sha256(timezone).hexdigest(),
    "packageVersions": packages,
}
(root / "manifest.json").write_text(json.dumps(summary, indent=2) + "\n")
print(json.dumps({key: summary[key] for key in ("imageID", "certificateCount", "timezoneVersionLine")}))
PY
