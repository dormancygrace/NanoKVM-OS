#!/usr/bin/env python3
"""Exercise the Alpine DHCP script and NanoKVM gateway hook with real routes.

Creates a disposable network namespace (see tests/fixtures/alpine-udhcpc).
No network access, device, privileges outside that namespace, or DHCP server
is needed.
"""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


def ip(*args):
    return subprocess.check_output(["ip", "-4", *args], text=True)


class GatewayHookTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nkos-dhcp-gateway-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / "udhcpc"
        self.config.mkdir()
        self.conf = self.config / "udhcpc.conf"
        self.conf.write_text('RESOLV_CONF="no"\n')
        self.preference = self.root / "gateway.preferred"
        self.preference.write_text("ethernet\n")

        hook = (ROOT / "server/service/network/scripts/99-nanokvm-gateway").read_text()
        hook = hook.replace("/etc/kvm/network/gateway.preferred", str(self.preference))
        hook = hook.replace("/etc/udhcpc/udhcpc.conf", str(self.conf))
        self.demoted = self.root / "gateway-demoted"
        self.demoted.mkdir()
        hook = hook.replace("/run/nanokvm/gateway-demoted", str(self.demoted))
        for event in ("bound", "renew"):
            directory = self.config / ("post-" + event)
            directory.mkdir()
            path = directory / "99-nanokvm-gateway"
            path.write_text(hook)
            path.chmod(0o755)

        alpine = (ROOT / "tests/fixtures/alpine-udhcpc/default.script").read_text()
        alpine = alpine.replace('UDHCPC="/etc/udhcpc"', 'UDHCPC="' + str(self.config) + '"')
        self.script = self.root / "default.script"
        self.script.write_text(alpine)
        ip("link", "add", "eth0", "type", "dummy")
        self.addCleanup(ip, "link", "del", "eth0")
        self.env = dict(
            os.environ, interface="eth0", ip="192.0.2.20", mask="24",
            subnet="255.255.255.0", router="192.0.2.1", dns="", domain="",
            search="", staticroutes="", NO_GATEWAY="", IF_METRIC="202",
        )

    def lease(self, event="bound", **options):
        subprocess.run(
            ["sh", str(self.script), event], env=dict(self.env, **options),
            input="", text=True, capture_output=True, check=True,
        )

    def defaults(self):
        return json.loads(ip("-j", "route", "show", "default", "dev", self.env["interface"]))

    def assert_gateways(self, expected, metric=10):
        routes = self.defaults()
        self.assertEqual({route.get("gateway") for route in routes}, set(expected), routes)
        for gateway in expected:
            self.assertTrue(
                any(r.get("gateway") == gateway and r.get("metric") == metric for r in routes),
                routes,
            )

    def test_router_option(self):
        self.lease()
        self.assert_gateways(["192.0.2.1"])

    def test_renew_keeps_a_demotion(self):
        # The gateway check demoted eth0; a renew keeps the route below the
        # others and records the preference's metric for the restore.
        self.lease()
        (self.demoted / "eth0").write_text("10\n")
        self.preference.write_text("ethernet\n")
        self.lease("renew")
        self.assert_gateways(["192.0.2.1"], metric=900)
        self.assertEqual((self.demoted / "eth0").read_text().strip(), "10")

    def test_auto_renew_keeps_a_demotion(self):
        self.preference.write_text("auto\n")
        self.lease()
        (self.demoted / "eth0").write_text("202\n")
        self.lease("renew")
        self.assert_gateways(["192.0.2.1"], metric=900)
        self.assertEqual((self.demoted / "eth0").read_text().strip(), "202")

    def test_one_default_route_per_gateway(self):
        # udhcpc installs the route with its own metric (202); the preference
        # must replace it, not leave a second default route beside it.
        for event in ("bound", "renew"):
            self.lease(event)
            routes = self.defaults()
            self.assertEqual([(r.get("gateway"), r.get("metric")) for r in routes],
                             [("192.0.2.1", 10)], routes)

    def test_failed_replace_keeps_the_lease_route(self):
        # Without the preferred route, the one udhcpc installed must stay.
        for event in ("bound", "renew"):
            hook = self.config / ("post-" + event) / "99-nanokvm-gateway"
            text = hook.read_text()
            self.assertIn("ip -4 route replace default", text)
            hook.write_text(text.replace("ip -4 route replace default", "false ip -4 route replace default"))
        self.lease()
        routes = self.defaults()
        self.assertEqual([(r.get("gateway"), r.get("metric")) for r in routes],
                         [("192.0.2.1", 202)], routes)

    def test_wifi_preference(self):
        ip("link", "add", "wlan0", "type", "dummy")
        self.addCleanup(ip, "link", "del", "wlan0")
        self.env["interface"] = "wlan0"
        self.preference.write_text("wifi\n")
        self.lease()
        self.assert_gateways(["192.0.2.1"])

    def test_multiple_classless_defaults_keep_order(self):
        self.lease(staticroutes="0.0.0.0/0 192.0.2.3 0.0.0.0/0 192.0.2.4")
        preferred = [r for r in self.defaults() if r.get("metric", 0) < 200]
        self.assertEqual(
            [(r["gateway"], r["metric"]) for r in preferred],
            [("192.0.2.3", 10), ("192.0.2.4", 11)],
        )

    def test_nonpreferred_interface(self):
        self.preference.write_text("wifi\n")
        self.lease()
        self.assert_gateways(["192.0.2.1"], metric=200)

    def test_classless_default_overrides_router(self):
        self.lease(staticroutes="198.51.100.0/24 192.0.2.2 0.0.0.0/0 192.0.2.3")
        self.assert_gateways(["192.0.2.3"])
        selected = json.loads(ip("-j", "route", "get", "203.0.113.7"))
        self.assertEqual(selected[0]["gateway"], "192.0.2.3")
        specific = json.loads(ip("-j", "route", "show", "198.51.100.0/24"))
        self.assertEqual(specific[0]["gateway"], "192.0.2.2")

    def test_classless_default_without_router_option(self):
        self.lease(router="", staticroutes="0.0.0.0/0 192.0.2.3")
        self.assert_gateways(["192.0.2.3"])

    def test_classless_routes_without_default(self):
        self.lease(staticroutes="198.51.100.0/24 192.0.2.2")
        self.assert_gateways([])

    def test_classless_zero_gateway(self):
        self.lease(staticroutes="0.0.0.0/0 0.0.0.0")
        self.assert_gateways([])

    def test_no_gateway_environment(self):
        self.lease(NO_GATEWAY="wlan0 eth0")
        self.assert_gateways([])

    def test_no_gateway_configuration_is_not_exported(self):
        with self.conf.open("a") as config:
            config.write('NO_GATEWAY="wlan0 eth0"\n')
        self.lease()
        self.assert_gateways([])

    def test_auto_keeps_alpine_metric(self):
        self.preference.write_text("auto\n")
        self.lease()
        self.assert_gateways(["192.0.2.1"], metric=202)

    def test_classless_renew_keeps_preference(self):
        options = dict(staticroutes="0.0.0.0/0 192.0.2.3")
        self.lease(**options)
        self.lease("renew", **options)
        self.lease("renew", **options)
        self.assert_gateways(["192.0.2.3"])

    def test_renew_prefers_new_gateway(self):
        self.lease()
        self.lease("renew", router="192.0.2.4")
        # Alpine owns the underlying routes; assert the effective route, not
        # cleanup of its old, higher-metric route on versions that retain it.
        selected = json.loads(ip("-j", "route", "get", "203.0.113.7"))
        self.assertEqual(selected[0]["gateway"], "192.0.2.4")
        preferred = [r for r in self.defaults() if r.get("metric") == 10]
        self.assertEqual([r["gateway"] for r in preferred], ["192.0.2.4"])


if __name__ == "__main__":
    if "--in-netns" not in sys.argv:
        env = dict(os.environ, NKOS_TEST_HOST_NETNS=os.readlink("/proc/self/ns/net"))
        flags = ["--net"] if os.geteuid() == 0 else ["--user", "--map-root-user", "--net"]
        raise SystemExit(subprocess.call(
            ["unshare", *flags, sys.executable, str(Path(__file__).resolve()), "--in-netns"],
            env=env,
        ))
    sys.argv.remove("--in-netns")
    original = os.environ.get("NKOS_TEST_HOST_NETNS")
    if not original or original == os.readlink("/proc/self/ns/net"):
        raise SystemExit("Refusing to change host routes: network namespace was not isolated.")
    unittest.main(verbosity=2)
