#!/bin/sh
# SPDX-FileCopyrightText: 2026 mnnoee
# SPDX-License-Identifier: Apache-2.0
#
# Build a Debian/Ubuntu package for one architecture.
# Usage: build/deb.sh [version] [arch] [binary] [output-dir]

set -eu

version="${1:-$(cat VERSION)}"
arch="${2:-amd64}"
binary="${3:-dist/jealous-vault-linux-$arch}"
output="${4:-dist}"

case "$arch" in
	amd64 | arm64) ;;
	*)
		echo "unsupported architecture: $arch (use amd64 or arm64)" >&2
		exit 1
		;;
esac

if [ ! -x "$binary" ]; then
	echo "binary not found or not executable: $binary" >&2
	exit 1
fi
if ! command -v dpkg-deb >/dev/null 2>&1; then
	echo "dpkg-deb is required to build the package" >&2
	exit 1
fi

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT INT TERM

mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/usr/share/doc/jealous-vault"
install -m 0755 "$binary" "$root/usr/bin/jealous-vault"
install -m 0644 LICENSE "$root/usr/share/doc/jealous-vault/copyright"
install -m 0644 NOTICE "$root/usr/share/doc/jealous-vault/NOTICE"
install -m 0644 README.md "$root/usr/share/doc/jealous-vault/README.md"
gzip -9n -c CHANGELOG.md >"$root/usr/share/doc/jealous-vault/changelog.gz"

installed_size="$(du -sk "$root/usr" | cut -f1)"
cat >"$root/DEBIAN/control" <<EOF
Package: jealous-vault
Version: $version
Architecture: $arch
Maintainer: mnnoee <me@mnnoee.ru>
Installed-Size: $installed_size
Depends: ca-certificates
Suggests: restic
Section: admin
Priority: optional
Homepage: https://jealousmoon.com/projects/vault/
Description: encrypted restic backups and predictable restores
 JealousVault wraps restic with one TOML config, systemd/OpenRC/cron
 scheduling, PostgreSQL and MariaDB dumps, integrity checks, test
 restores and alerts. The binary is static. restic is a runtime
 dependency but is not pulled in by the package; install it separately.
EOF

mkdir -p "$output"
package="$output/jealous-vault_${version}_${arch}.deb"
dpkg-deb --build --root-owner-group "$root" "$package" >/dev/null
echo "$package"
