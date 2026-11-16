#!/bin/sh
set -eu

# Architecture-independent Debian security data. These hashes were verified
# against Bookworm's signed security Packages index on 2026-11-16. Keep the
# payload and its dpkg metadata together so the image inventory stays truthful.
root="${1:-/runtime}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/payload" "$work/certs" "$work/empty" \
    "$root/etc/ssl/certs" "$root/usr/share/doc" "$root/var/lib/dpkg/status.d"

download() {
    package="$1"
    version="$2"
    path="$3"
    checksum="$4"
    archive="$work/$package.deb"
    curl --fail --location --retry 3 "https://security.debian.org/debian-security/$path" -o "$archive"
    printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check
    test "$(dpkg-deb --field "$archive" Package)" = "$package"
    test "$(dpkg-deb --field "$archive" Version)" = "$version"
    test "$(dpkg-deb --field "$archive" Architecture)" = all
    dpkg-deb --extract "$archive" "$work/payload"
    dpkg-deb --control "$archive" "$work/control-$package"
    {
        cat "$work/control-$package/control"
        printf 'Status: install ok installed\n'
    } > "$root/var/lib/dpkg/status.d/$package"
    cp "$work/control-$package/md5sums" "$root/var/lib/dpkg/status.d/$package.md5sums"
    mkdir -p "$root/usr/share/doc/$package"
    cp "$work/payload/usr/share/doc/$package/copyright" "$root/usr/share/doc/$package/copyright"
}

download ca-certificates '20250419~deb12u2' \
    'pool/updates/main/c/ca-certificates/ca-certificates_20250419~deb12u2_all.deb' \
    b9f76b5271a7ac373fd5e7e1b5b826a71ddd702b42a0d104955ecb1acfffc94b
download tzdata '2026f-0+deb12u1' \
    'pool/updates/main/t/tzdata/tzdata_2026f-0+deb12u1_all.deb' \
    3254eb870c8a949518214805aee5e4276af7699988ea8b9a3a583cd0f1eab31c

# Generate the bundle using Debian's own helper with only the packaged roots.
# Do not import certificates or configuration from the build host. The pinned
# distroless base contains only this bundle, not individual CA files/symlinks.
: > "$work/ca-certificates.conf"
LC_ALL=C sh "$work/payload/usr/sbin/update-ca-certificates" --default \
    --certsconf "$work/ca-certificates.conf" \
    --certsdir "$work/payload/usr/share/ca-certificates" \
    --localcertsdir "$work/empty" --hooksdir "$work/empty" \
    --etccertsdir "$work/certs"
test -s "$work/certs/ca-certificates.crt"
cp "$work/certs/ca-certificates.crt" "$root/etc/ssl/certs/ca-certificates.crt"
cp -a "$work/payload/usr/share/zoneinfo" "$root/usr/share/"
