#!/bin/sh
# Checks the Linux kernel gate in install.sh against real release strings.
#
# It extracts kernel_supported() from install.sh and runs only that function.
# install.sh itself is never executed: it downloads and installs a binary.
#
#   sh scripts/test-install-kernel.sh
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

sed -n '/^kernel_supported()/,/^}/p' "$root/install.sh" > "$tmp/fn.sh"
if [ ! -s "$tmp/fn.sh" ]; then
	echo "FAIL: kernel_supported() not found in install.sh" >&2
	exit 1
fi
# shellcheck source=/dev/null
. "$tmp/fn.sh"

failures=0
# check <release> <yes|no>: whether the release is accepted.
check() {
	if kernel_supported "$1"; then got=yes; else got=no; fi
	if [ "$got" = "$2" ]; then
		echo "PASS  [$1] -> $got"
	else
		echo "FAIL  [$1] -> $got, want $2"
		failures=$((failures + 1))
	fi
}

# Older than 3.10 is refused. The two neighbours of the boundary matter most:
# 3.9 must lose to 3.10 on the number, not on the string.
check 2.6.32-754.el6 no
check 3.9.0 no
check 3.10.0-1160.el7 yes
check 3.18.140 yes
check 4.19.0-27 yes
check 5.15.0-91-generic yes
check 6.8.0-45-generic yes
check 6.18.44-fc-v726 yes
check 7.0.1 yes
# A string that cannot be read is allowed through, not refused.
check garbage yes
check 6 yes
check "" yes

if [ "$failures" -ne 0 ]; then
	echo "$failures check(s) failed" >&2
	exit 1
fi
echo "all kernel checks passed"
