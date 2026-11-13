#!/bin/sh
set -eu

# Source builds omit optional libpq authentication/discovery integrations. TLS,
# SCRAM and the full zlib archive format remain available.
apt-get update -qq
apt-get install -y --no-install-recommends curl bzip2 bison flex libssl-dev=3.0.22-1~deb12u2
mkdir -p /sources /patched/lib /patched/usr/share/doc/pgbase-postgresql-source /patched/var/lib/dpkg/status.d
cd /sources
curl --fail --location --retry 3 https://ftp.postgresql.org/pub/source/v18.7/postgresql-18.7.tar.bz2 -o postgresql.tar.bz2
echo '441dfae4280cae3e0425e5f876f80559fb0731c140f1aae22b3e5a54d0411bfd  postgresql.tar.bz2' | sha256sum --check
curl --fail --location --retry 3 https://zlib.net/zlib-1.4.tar.gz -o zlib.tar.gz
echo '02dd159783677a749c04adbdd02b55b8a2bcc7e054f51b76b549199a916ad584  zlib.tar.gz' | sha256sum --check
tar -xf postgresql.tar.bz2
tar -xf zlib.tar.gz
cd /sources/postgresql-18.7
./configure --prefix=/patched --without-icu --without-readline --without-zlib --without-gssapi --without-ldap --with-ssl=openssl
make -j2 -C src/interfaces/libpq
cp -L src/interfaces/libpq/libpq.so.5 /patched/lib/libpq.so.5
cp COPYRIGHT /patched/usr/share/doc/pgbase-postgresql-source/postgresql-COPYRIGHT
cd /sources/zlib-1.4
./configure --prefix=/patched
make -j2
make test
cp -L libz.so.1 /patched/lib/libz.so.1
cp LICENSE /patched/usr/share/doc/pgbase-postgresql-source/zlib-LICENSE
# Keep the actual Debian version/attribution for the patched OpenSSL libraries.
for library in libssl.so.3 libcrypto.so.3; do
    path="$(ldconfig -p | awk -v library="$library" '$1 == library {print $NF; exit}')"
    cp -L "$path" /patched/lib/"$library"
done
cp -L /usr/share/doc/libssl3/copyright /patched/usr/share/doc/pgbase-postgresql-source/openssl-copyright
dpkg-query --status libssl3 > /patched/var/lib/dpkg/status.d/libssl3
cat > /patched/usr/share/doc/pgbase-postgresql-source/sbom.spdx.json <<'SPDX'
{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"pgbase-postgresql-client-source-libraries","documentNamespace":"https://github.com/yakisoba0728/PgBase/spdx/postgresql18.7-zlib1.4","creationInfo":{"creators":["Organization: PgBase"],"created":"2026-11-14T00:00:00Z"},"packages":[{"SPDXID":"SPDXRef-libpq","name":"libpq","versionInfo":"18.7","downloadLocation":"https://ftp.postgresql.org/pub/source/v18.7/postgresql-18.7.tar.bz2","filesAnalyzed":false,"licenseDeclared":"PostgreSQL","checksums":[{"algorithm":"SHA256","checksumValue":"441dfae4280cae3e0425e5f876f80559fb0731c140f1aae22b3e5a54d0411bfd"}],"externalRefs":[{"referenceCategory":"PACKAGE-MANAGER","referenceType":"purl","referenceLocator":"pkg:generic/postgresql/libpq@18.7"}]},{"SPDXID":"SPDXRef-zlib","name":"zlib","versionInfo":"1.4","downloadLocation":"https://zlib.net/zlib-1.4.tar.gz","filesAnalyzed":false,"licenseDeclared":"Zlib","checksums":[{"algorithm":"SHA256","checksumValue":"02dd159783677a749c04adbdd02b55b8a2bcc7e054f51b76b549199a916ad584"}],"externalRefs":[{"referenceCategory":"PACKAGE-MANAGER","referenceType":"purl","referenceLocator":"pkg:generic/zlib@1.4"}]}],"relationships":[{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-libpq"},{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-zlib"}]}
SPDX
