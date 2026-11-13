#!/bin/sh
set -eu

major="$1"
root=/runtime
mkdir -p "$root/etc"
# Distroless static's compat lookup needs an absent NSS module; glibc clients
# use its built-in files/DNS providers for local identities and host lookup.
printf 'passwd: files\ngroup: files\nshadow: files\nhosts: files dns\n' > "$root/etc/nsswitch.conf"
for program in pg_dump pg_restore; do
    binary="/usr/lib/postgresql/$major/bin/$program"
    # ldd includes the transitive ELF dependencies and architecture's loader.
    libraries="$(ldd "$binary" | awk '$2 == "=>" && $3 ~ /^\// {print $3} $1 ~ /^\// {print $1}')"
    for path in "$binary" $libraries; do
        if [ "$path" = "$binary" ]; then
            mkdir -p "$root/opt/pgbase/postgresql/$major/bin"
            cp "$path" "$root/opt/pgbase/postgresql/$major/bin/$program"
        else
            cp --parents --dereference "$path" "$root"
        fi
        # Custom source libraries carry their own truthful SPDX/license records.
        case "$path" in /opt/pgbase/postgresql/lib/*) continue ;; esac
        package="$(dpkg-query --search "$path" "$(readlink -f "$path")" 2>/dev/null | head -n 1 | cut -d: -f1)"
        test -n "$package"
        documentation="$root/usr/share/doc/pgbase-postgresql-$major/$package"
        mkdir -p "$documentation" "$root/var/lib/dpkg/status.d"
        cp -L "/usr/share/doc/$package/copyright" "$documentation/copyright"
        dpkg-query --status "$package" > "$root/var/lib/dpkg/status.d/$package"
    done
done
