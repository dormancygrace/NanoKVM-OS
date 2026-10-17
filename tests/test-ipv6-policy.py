#!/usr/bin/env python3
"""Exercise IPv6 apply/rollback and service restarts with real Linux routes.

No host/device interfaces are touched. Run with unshare support, or as root
inside CI; the child refuses to execute outside its own network namespace.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "server/service/network/scripts/S02ipv6"


class IPv6PolicyTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nkos-ipv6-")
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        for name in ("boot", "etc", "run"):
            (self.work / name).mkdir()
        self.conf = self.work / "ipv6"
        self.env = dict(os.environ,
            NANOKVM_IPV6_CONF=str(self.conf), NANOKVM_IPV6_RUN=str(self.work / "run"),
            NANOKVM_DHCPV6="/bin/true", NANOKVM_IPV6_INIT=str(SCRIPT),
            NANOKVM_ETH_CONFIG=str(self.work / "boot/eth.nodhcp"),
            NANOKVM_ETH_DISABLED=str(self.work / "boot/eth.disabled"),
            NANOKVM_ETH_DHCP_PID=str(self.work / "run/udhcpc.eth0.pid"),
            NANOKVM_ETH_VLAN=str(self.work / "boot/eth.vlan"),
            NANOKVM_ETH_VLAN_STATE=str(self.work / "run/eth.vlan"),
            NANOKVM_HOSTNAME_FILE=str(self.work / "hostname"),
            NANOKVM_UDHCPC="/bin/true", NANOKVM_ARPING="/bin/true",
            NANOKVM_DNS_MODE_FILE=str(self.work / "etc/dns.mode"),
            NANOKVM_RESOLV_CONF=str(self.work / "resolv.conf"),
            NANOKVM_GATEWAY_PREFERENCE_FILE=str(self.work / "etc/gateway.preferred"),
            NANOKVM_WIFI_BOOT_DIR=str(self.work / "boot"),
            NANOKVM_WIFI_ETC_DIR=str(self.work / "etc"),
            NANOKVM_WIFI_RUN_DIR=str(self.work / "run/wifi"),
            NANOKVM_WIFI_DHCP_PID=str(self.work / "run/udhcpc.wlan0.pid"),
            NANOKVM_WIFI_AP_FLAG=str(self.work / "run/ap"), NANOKVM_KILLALL="/bin/true")
        for name in ("eth0", "wlan0"):
            self.run_cmd("ip", "link", "add", name, "type", "dummy")
            self.addCleanup(self.run_cmd, "ip", "link", "del", name)
            self.run_cmd("ip", "link", "set", name, "up")

    def run_cmd(self, *args, check=True):
        return subprocess.run(args, env=self.env, text=True, capture_output=True, check=check)

    def apply(self, address, gateway, iface="eth0", check=True):
        return self.run_cmd("sh", str(SCRIPT), "apply", iface + "=static",
            iface + ".address=" + address, iface + ".gateway=" + gateway, check=check)

    def addresses(self, iface):
        data = json.loads(self.run_cmd("ip", "-j", "-6", "addr", "show", "dev", iface).stdout)
        return {a["local"] for item in data for a in item["addr_info"] if a["scope"] == "global"}

    def gateways(self, iface):
        data = json.loads(self.run_cmd("ip", "-j", "-6", "route", "show", "default", "dev", iface).stdout)
        return {r.get("gateway") for r in data}

    def dhcp_event(self, event, **options):
        script = self.work / "dhcpv6-event"
        source = (ROOT / "server/service/network/scripts/nanokvm-dhcpv6-event").read_text()
        script.write_text(source.replace("/etc/kvm/network/dns.mode", str(self.work / "etc/dns.mode"))
            .replace("/boot/resolv.conf", str(self.work / "boot/resolv.conf")))
        bin_dir = self.work / "bin"
        bin_dir.mkdir(exist_ok=True)
        resolver = bin_dir / "resolvconf"
        resolver.write_text('#!/bin/sh\ncase "$1" in\n-a) cat > "$NANOKVM_TEST_DNS_DIR/$2" ;;\n-d) rm -f "$NANOKVM_TEST_DNS_DIR/$2" ;;\n*) exit 1 ;;\nesac\n')
        resolver.chmod(0o755)
        env = dict(self.env, PATH=str(bin_dir) + ":" + os.environ["PATH"],
            NANOKVM_DHCPV6_RUN=str(self.work / "run"), NANOKVM_TEST_DNS_DIR=str(self.work / "run"),
            interface="eth0", ipv6="2001:db8::10", lease="3600", dns="2001:db8::53", search="example.test")
        env.update(options)
        subprocess.run(["sh", str(script), event], env=env, check=True, capture_output=True)

    def test_dhcp_renew_and_deconfig_preserve_unowned_addresses(self):
        self.run_cmd("ip", "-6", "addr", "add", "2001:db8::99/64", "dev", "eth0")
        self.dhcp_event("bound")
        self.assertEqual(self.addresses("eth0"), {"2001:db8::10", "2001:db8::99"})
        self.dhcp_event("renew", ipv6="2001:db8::20")
        self.assertEqual(self.addresses("eth0"), {"2001:db8::20", "2001:db8::99"})
        self.dhcp_event("deconfig")
        self.assertEqual(self.addresses("eth0"), {"2001:db8::99"})
        self.assertFalse((self.work / "run/udhcpc6.eth0.addr").exists())
        self.assertFalse((self.work / "run/nkos.dhcpv6.eth0").exists())

    def test_dhcp_renew_withdraws_obsolete_dns(self):
        key = self.work / "run/nkos.dhcpv6.eth0"
        self.dhcp_event("bound")
        self.assertIn("nameserver 2001:db8::53", key.read_text())
        self.dhcp_event("renew", dns="")
        self.assertFalse(key.exists())
        self.dhcp_event("renew")
        (self.work / "etc/dns.mode").write_text("manual\n")
        self.dhcp_event("renew")
        self.assertFalse(key.exists())
        self.assertEqual(self.addresses("eth0"), {"2001:db8::10"})

    def test_failed_gateway_rolls_back_without_leaking_address(self):
        self.apply("2001:db8:1::10/64", "2001:db8:1::1")
        failed = self.apply("2001:db8:2::20/64", "2001:db8:3::1", check=False)
        self.assertNotEqual(failed.returncode, 0)
        self.apply("2001:db8:1::10/64", "2001:db8:1::1")
        self.assertEqual(self.addresses("eth0"), {"2001:db8:1::10"})
        self.assertEqual(self.gateways("eth0"), {"2001:db8:1::1"})

    def test_ethernet_restart_restores_static_ipv6(self):
        self.conf.write_text("eth0=static eth0.address=2001:db8:1::10/64 eth0.gateway=2001:db8:1::1\n")
        self.apply("2001:db8:1::10/64", "2001:db8:1::1")
        self.run_cmd("sh", str(ROOT / "kvmapp/system/init.d/S30eth"), "restart")
        self.assertEqual(self.addresses("eth0"), {"2001:db8:1::10"})
        self.assertEqual(self.gateways("eth0"), {"2001:db8:1::1"})

    def test_wifi_disable_start_restores_static_ipv6(self):
        self.conf.write_text("wlan0=static wlan0.address=2001:db8:2::20/64 wlan0.gateway=2001:db8:2::1\n")
        self.apply("2001:db8:2::20/64", "2001:db8:2::1", "wlan0")
        service = str(ROOT / "kvmapp/system/init.d/S30wifi")
        self.run_cmd("sh", service, "disable")
        self.assertEqual(self.addresses("wlan0"), set())
        self.run_cmd("sh", service, "start")
        self.assertEqual(self.addresses("wlan0"), {"2001:db8:2::20"})
        self.assertEqual(self.gateways("wlan0"), {"2001:db8:2::1"})

    def test_static_policy_can_be_saved_while_interface_is_down(self):
        self.run_cmd("ip", "link", "set", "wlan0", "down")
        self.apply("2001:db8:2::20/64", "2001:db8:2::1", "wlan0")
        self.assertEqual(self.addresses("wlan0"), set())
        self.conf.write_text("wlan0=static wlan0.address=2001:db8:2::20/64 wlan0.gateway=2001:db8:2::1\n")
        self.run_cmd("sh", str(ROOT / "kvmapp/system/init.d/S30wifi"), "start")
        self.assertEqual(self.addresses("wlan0"), {"2001:db8:2::20"})
        self.assertEqual(self.gateways("wlan0"), {"2001:db8:2::1"})

    def test_static_to_automatic_removes_owned_address_and_route(self):
        self.apply("2001:db8:1::10/64", "2001:db8:1::1")
        self.run_cmd("sh", str(SCRIPT), "apply", "eth0=1")
        self.assertEqual(self.addresses("eth0"), set())
        self.assertEqual(self.gateways("eth0"), set())
        self.assertEqual(Path("/proc/sys/net/ipv6/conf/eth0/autoconf").read_text().strip(), "1")


if __name__ == "__main__":
    if "--in-netns" not in sys.argv:
        env = dict(os.environ, NKOS_TEST_HOST_NETNS=os.readlink("/proc/self/ns/net"))
        flags = ["--net"] if os.geteuid() == 0 else ["--user", "--map-root-user", "--net"]
        raise SystemExit(subprocess.call(["unshare", *flags, sys.executable,
            str(Path(__file__).resolve()), "--in-netns"], env=env))
    sys.argv.remove("--in-netns")
    original = os.environ.get("NKOS_TEST_HOST_NETNS")
    if not original or original == os.readlink("/proc/self/ns/net"):
        raise SystemExit("Refusing to change host routes: network namespace was not isolated.")
    unittest.main(verbosity=2)
