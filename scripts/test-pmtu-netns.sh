#!/bin/sh
# Run an already compiled pathmtu integration test binary in a disposable namespace.
set -eu
[ "$#" -eq 1 ] || { echo "usage: $0 /absolute/path/to/pathmtu.test" >&2; exit 2; }
case "$1" in /*) ;; *) echo "test binary must have an absolute path" >&2; exit 2;; esac
exec "${NANOKVM_PMTU_UNSHARE:-unshare}" -n /bin/sh -eu -c '
 printf 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6
 printf 0 > /proc/sys/net/ipv6/conf/default/disable_ipv6
 ip link set lo up
 ip addr add 198.18.0.1/32 dev lo
 ip -6 addr add fd77:706d:7475::1/128 dev lo nodad
 export NANOKVM_PMTU_NETNS_TEST=1
 exec "$1" -test.v -test.count=1 -test.parallel=2 -test.timeout=90s -test.run="${NANOKVM_PMTU_TEST_RUN:-.}"
' pmtu-netns "$1"
