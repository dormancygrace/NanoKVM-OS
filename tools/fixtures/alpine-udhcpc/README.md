# Alpine udhcpc test fixture

Unmodified Alpine BusyBox DHCP event script from
https://github.com/alpinelinux/aports/blob/e0e709e5b3aa2aaeb9db35590bfd2aedd7f0882e/main/busybox/default.script

SHA-256: c7b39bba4d7f1ce05dbdeba7d2fd97b96a8c3c0c1ebc0259f641fb8e1c3827d2

This is a test fixture, not a firmware overlay. It exercises the server's
post-bound/post-renew gateway hook against Alpine's route selection, including
RFC3442 option 121 and NO_GATEWAY. The test rewrites only the configuration
directory into a temporary directory and runs iproute2 in an isolated network
namespace. Keep the upstream copyright notice in the script.

The test creates a disposable network namespace automatically:

    python3 tools/test-dhcp-gateway.py

On hosts that restrict unprivileged namespaces:

    sudo python3 tools/test-dhcp-gateway.py
