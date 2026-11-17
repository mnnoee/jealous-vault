#!/bin/sh
# SPDX-FileCopyrightText: 2026 mnnoee
# SPDX-License-Identifier: Apache-2.0
#
# Verify a JealousVault release directory. Run it from anywhere:
#   build/verify-release.sh /path/to/extracted/release
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <release-dir>" >&2
	exit 2
fi

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$1"

ssh-keygen -Y verify \
	-f "$dir/allowed_signers" \
	-I release \
	-n jealous-vault-release \
	-s SHA256SUMS.sig <SHA256SUMS
sha256sum -c SHA256SUMS
