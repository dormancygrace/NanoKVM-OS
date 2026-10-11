#!/bin/sh
# nanokvm-serial-console: a login console on the NanoKVM USB serial port.
#
# Runs on the computer that the NanoKVM is plugged into (the "host"), not on
# the NanoKVM. The NanoKVM's USB serial function is a CDC ACM device
# (3346:1009); Linux names it /dev/ttyACMn. This script finds out what the
# host runs and either sets up a getty on that port or says why it will not.
#
#   nanokvm-serial-console.sh [plan]             report, change nothing (default)
#   nanokvm-serial-console.sh status             short state of the setup
#   nanokvm-serial-console.sh apply [--serial S] set the console up
#   nanokvm-serial-console.sh undo               remove what apply added
#
# Options: --serial SERIAL  the NanoKVM's USB serial number (the web UI shows
#                           it; needed when the device is not plugged in now)
#          --yes            apply without the confirmation prompt
#
# plan and status only read. apply writes new files and appends to inittab
# with a backup; it never edits an existing file in place. Every change is
# recorded in a manifest, and undo reverses it. A second apply changes
# nothing. Exit codes: 0 done or nothing to do, 1 error, 2 refused as unsafe,
# 3 unsupported system (manual steps printed).
#
# POSIX sh (dash, busybox ash, bash). Tests run it against a fake root:
# NANOKVM_ROOT prefixes every path, and the commands below come from PATH.

set -u

VERSION=1
VID=3346
PID=1009
ROOT=${NANOKVM_ROOT:-}
RULE_FILE=/etc/udev/rules.d/90-nanokvm-serial-console.rules
BACKUP_DIR=/var/backups/nanokvm-serial-console
INITTAB_TAG='# nanokvm-serial-console'
TTY_BAUD=115200
TTY_TERM=vt220

action=plan
serial=
assume_yes=0

usage() {
	sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

while [ $# -gt 0 ]; do
	case $1 in
	plan | status | apply | undo) action=$1 ;;
	--serial)
		[ $# -ge 2 ] || usage 1
		serial=$2
		shift
		;;
	--serial=*) serial=${1#--serial=} ;;
	--yes | -y) assume_yes=1 ;;
	-h | --help) usage 0 ;;
	*)
		echo "unknown argument: $1" >&2
		usage 1
		;;
	esac
	shift
done

# ---------------------------------------------------------------- reporting
warnings=0
refusals=0
say() { printf '%s\n' "$*"; }
ok() { say "ok:      $*"; }
info() { say "info:    $*"; }
warn() {
	say "warn:    $*"
	warnings=$((warnings + 1))
}
refuse() {
	say "refuse:  $*"
	refusals=$((refusals + 1))
}
fail() {
	say "error:   $*" >&2
	exit 1
}

# ------------------------------------------------------------ small helpers
readfile() { [ -r "$ROOT$1" ] && cat "$ROOT$1" 2>/dev/null; }
exists() { [ -e "$ROOT$1" ]; }
have() { command -v "$1" >/dev/null 2>&1; }
# os-release values are shell-like; read them without executing the file.
osr() {
	readfile /etc/os-release | sed -n "s/^$1=//p" | head -n 1 | sed 's/^"//; s/"$//; s/^'"'"'//; s/'"'"'$//'
}
kernel_release() {
	if [ -n "${NANOKVM_KERNEL:-}" ]; then
		say "$NANOKVM_KERNEL"
	else
		uname -r
	fi
}

# ------------------------------------------------------------ what is here
distro_id=$(osr ID)
distro_like=$(osr ID_LIKE)
distro_name=$(osr PRETTY_NAME)
[ -n "$distro_id" ] || distro_id=unknown
[ -n "$distro_name" ] || distro_name="unknown (no /etc/os-release)"

# init system: what runs as PID 1
init_comm=$(readfile /proc/1/comm)
init=unknown
case $init_comm in
systemd) init=systemd ;;
*)
	if exists /run/systemd/system; then
		init=systemd
	elif exists /run/runit || exists /etc/runit/1; then
		init=runit
	elif exists /run/openrc/softlevel && ! exists /etc/inittab; then
		init=openrc
	elif exists /etc/inittab; then
		# sysvinit, busybox init (Alpine), OpenRC on top of sysvinit (Gentoo)
		init=inittab
	fi
	;;
esac

# busybox init (Alpine) reads another inittab line form than sysvinit
init_busybox=
for p in /proc/1/exe /sbin/init; do
	case $(readlink "$ROOT$p" 2>/dev/null) in *busybox*) init_busybox=yes ;; esac
done

# container: udev does not run here, and the USB device belongs to the host
container=
if exists /.dockerenv || exists /run/.containerenv; then
	container=yes
elif readfile /proc/1/environ | tr '\0' '\n' | grep -q '^container='; then
	container=yes
elif readfile /proc/1/cgroup | grep -qE '/(lxc|docker|machine\.slice/machine-)'; then
	container=yes
fi

# immutable or read-only system
immutable=
if exists /run/ostree-booted; then
	immutable="OSTree (rpm-ostree, Silverblue, CoreOS)"
elif [ "$distro_id" = nixos ] || exists /etc/NIXOS; then
	immutable="NixOS: /etc is generated from the configuration"
elif readfile /proc/mounts | awk '$2 == "/" { print $4 }' | grep -qE '(^|,)ro(,|$)'; then
	immutable="the root file system is mounted read-only"
fi

# cdc_acm driver for the running kernel
krel=$(kernel_release)
cdc_acm=
if exists /sys/bus/usb/drivers/cdc_acm || exists /sys/module/cdc_acm; then
	cdc_acm=loaded
elif readfile "/lib/modules/$krel/modules.builtin" | grep -q 'cdc-acm'; then
	cdc_acm=builtin
else
	# cdc-acm.ko, or compressed: .ko.xz, .ko.zst, .ko.gz
	for m in "$ROOT/lib/modules/$krel/kernel/drivers/usb/class/cdc-acm.ko"* \
		"$ROOT/usr/lib/modules/$krel/kernel/drivers/usb/class/cdc-acm.ko"*; do
		[ -e "$m" ] && cdc_acm=module && break
	done
fi

# getty
agetty=
for p in /sbin/agetty /usr/sbin/agetty /usr/bin/agetty /bin/agetty; do
	[ -x "$ROOT$p" ] && agetty=$p && break
done
getty=
for p in /sbin/getty /usr/sbin/getty /bin/getty /usr/bin/getty; do
	[ -x "$ROOT$p" ] && getty=$p && break
done
serial_getty_unit=
for p in /lib/systemd/system/serial-getty@.service /usr/lib/systemd/system/serial-getty@.service /etc/systemd/system/serial-getty@.service; do
	exists "$p" && serial_getty_unit=$p && break
done

# the NanoKVM on the USB bus: VID/PID, serial, its tty
dev_serial=
dev_tty=
dev_path=
other_acm=
for d in "$ROOT"/sys/bus/usb/devices/*; do
	[ -r "$d/idVendor" ] || continue
	if [ "$(cat "$d/idVendor" 2>/dev/null)" = "$VID" ] && [ "$(cat "$d/idProduct" 2>/dev/null)" = "$PID" ]; then
		dev_path=${d#"$ROOT"}
		dev_serial=$(cat "$d/serial" 2>/dev/null)
		for t in "$d"/*/tty/ttyACM*; do
			[ -e "$t" ] && dev_tty=${t##*/} && break
		done
	fi
done
for t in "$ROOT"/sys/class/tty/ttyACM*; do
	[ -e "$t" ] || continue
	n=${t##*/}
	[ "$n" = "$dev_tty" ] || other_acm="$other_acm $n"
done
if [ -n "$serial" ] && [ -n "$dev_serial" ] && [ "$serial" != "$dev_serial" ]; then
	warn "--serial $serial differs from the connected NanoKVM ($dev_serial); using the connected one"
fi
[ -n "$dev_serial" ] && serial=$dev_serial

# the setup as it is now
rule_present=
rule_serial=
if exists "$RULE_FILE"; then
	rule_present=yes
	rule_serial=$(readfile "$RULE_FILE" | sed -n 's/.*ATTRS{serial}=="\([^"]*\)".*/\1/p' | head -n 1)
fi
# A login set up another way (by hand, after the docs): a udev rule of
# someone else's that starts a getty for this NanoKVM, or a getty already
# running on its port. That setup is theirs; this script leaves it alone.
foreign_rule=
foreign_getty=
for f in "$ROOT"/etc/udev/rules.d/*.rules "$ROOT"/lib/udev/rules.d/*.rules "$ROOT"/usr/lib/udev/rules.d/*.rules; do
	[ -r "$f" ] || continue
	[ "${f#"$ROOT"}" = "$RULE_FILE" ] && continue
	if grep -q 'serial-getty@' "$f" && grep -qE "idVendor\"?\}?==\"$VID\"" "$f" && grep -qE "idProduct\"?\}?==\"$PID\"" "$f"; then
		if [ -z "$serial" ] || ! grep -q 'ATTRS{serial}==' "$f" || grep -q "ATTRS{serial}==\"$serial\"" "$f"; then
			foreign_rule=${f#"$ROOT"}
			break
		fi
	fi
done
# systemd keeps this symlink while the unit is active
if [ -n "$dev_tty" ] && [ -L "$ROOT/run/systemd/units/invocation:serial-getty@$dev_tty.service" ]; then
	foreign_getty="serial-getty@$dev_tty.service"
fi
inittab_present=
if exists /etc/inittab && readfile /etc/inittab | grep -qF "$INITTAB_TAG"; then
	inittab_present=yes
fi
modemmanager=
for p in /usr/sbin/ModemManager /usr/bin/ModemManager /usr/libexec/ModemManager; do
	[ -e "$ROOT$p" ] && modemmanager=yes && break
done
selinux=
[ -e "$ROOT/sys/fs/selinux/enforce" ] && selinux=$(cat "$ROOT/sys/fs/selinux/enforce" 2>/dev/null)

# ------------------------------------------------------------------ report
report() {
	say "nanokvm-serial-console $VERSION: $action"
	say "system:  $distro_name (id=$distro_id${distro_like:+, like $distro_like})"
	say "init:    $init${init_comm:+ (PID 1: $init_comm)}"
	say "kernel:  $krel"
	if [ -n "$dev_tty" ]; then
		ok "NanoKVM $VID:$PID is connected as /dev/$dev_tty (serial $dev_serial)"
	elif [ -n "$dev_path" ]; then
		warn "NanoKVM $VID:$PID is connected at $dev_path but has no ttyACM: USB serial is not enabled on it, or cdc_acm is not loaded"
	elif [ -n "$serial" ]; then
		info "NanoKVM is not connected now; using serial $serial from --serial"
	else
		warn "NanoKVM is not connected now and no --serial given: the rule cannot be bound to its serial number"
	fi
	[ -n "$other_acm" ] && info "other CDC ACM ports:$other_acm (numbering may change; the rule matches the NanoKVM by serial, not by name)"

	[ -n "$container" ] && refuse "this is a container: udev and the USB device belong to the host, set the console up there"
	[ -n "$immutable" ] && refuse "$immutable; the steps at the end are for your configuration instead"
	case $cdc_acm in
	loaded) ok "cdc_acm driver is loaded" ;;
	builtin) ok "cdc_acm is built into the kernel" ;;
	module) ok "cdc_acm module is available for $krel (loads when the device appears)" ;;
	*) refuse "no cdc_acm driver for kernel $krel: the port cannot appear on this host" ;;
	esac
	case $init in
	systemd)
		if [ -n "$serial_getty_unit" ]; then
			ok "systemd with serial-getty@.service ($serial_getty_unit)"
		else
			refuse "systemd without serial-getty@.service; install the package that provides it (systemd itself on most distributions)"
		fi
		[ -n "$agetty" ] || refuse "no agetty (util-linux) found"
		;;
	inittab)
		if [ -n "$agetty" ] || [ -n "$getty" ]; then
			ok "${init_busybox:+busybox }init with /etc/inittab, getty ${agetty:-$getty}"
		else
			refuse "no getty or agetty found"
		fi
		[ -n "$dev_tty" ] || warn "inittab names a fixed device; without the NanoKVM connected the line is written for ttyACM0"
		;;
	runit | openrc)
		refuse "$init as PID 1 is not handled by this version; the steps at the end describe the manual setup"
		;;
	*)
		refuse "unknown init system; the steps at the end describe the manual setup"
		;;
	esac
	[ -n "$modemmanager" ] && info "ModemManager is installed; the rule marks the port so it is not probed"
	[ "$selinux" = 1 ] && info "SELinux is enforcing; the new files get their default labels (restorecon)"
	if [ -n "$rule_present" ]; then
		if [ -n "$rule_serial" ] && [ -n "$serial" ] && [ "$rule_serial" != "$serial" ]; then
			warn "$RULE_FILE is present for serial $rule_serial, not $serial"
		else
			ok "$RULE_FILE is present${rule_serial:+ (serial $rule_serial)}"
		fi
	fi
	[ -n "$inittab_present" ] && ok "/etc/inittab has the console line"
	[ -n "$foreign_rule" ] && ok "a login for this NanoKVM is already set up by $foreign_rule (not by this script)"
	[ -n "$foreign_getty" ] && ok "$foreign_getty is running on /dev/$dev_tty"
}

manual_steps() {
	say ""
	say "Manual setup, for when this script refuses:"
	say "  1. Make sure the cdc_acm driver is available; the NanoKVM appears as /dev/ttyACMn."
	say "  2. Start a login on it. With systemd: a udev rule in /etc/udev/rules.d such as"
	say "       ACTION==\"add\", SUBSYSTEM==\"tty\", KERNEL==\"ttyACM[0-9]*\", ATTRS{idVendor}==\"$VID\", ATTRS{idProduct}==\"$PID\", ATTRS{serial}==\"${serial:-SERIAL}\", TAG+=\"systemd\", ENV{SYSTEMD_WANTS}+=\"serial-getty@%k.service\""
	say "     With an inittab: a respawn line that runs getty on the port."
	say "  3. The line speed is nominal: CDC ACM carries bytes at USB speed whatever the baud rate."
	say "  Early boot output (firmware, the kernel before USB is up) cannot go through CDC ACM."
}

planned_changes() {
	case $init in
	systemd)
		say "plan:    write $RULE_FILE (udev rule: start serial-getty@<port> for NanoKVM serial ${serial:-<unknown>})"
		say "plan:    udevadm control --reload, then trigger the add event for ${dev_tty:-the port when it appears}"
		;;
	inittab)
		say "plan:    append one respawn line for /dev/${dev_tty:-ttyACM0} to /etc/inittab (backup kept), then tell init to reread it"
		;;
	esac
	say "plan:    record the change in $BACKUP_DIR/<timestamp>/ for undo"
}

# ----------------------------------------------------------- write helpers
manifest=
stamp() { date +%Y%m%d-%H%M%S; }
start_manifest() {
	mkdir -p "$ROOT$BACKUP_DIR" || fail "cannot create $BACKUP_DIR"
	manifest=$BACKUP_DIR/$(stamp)
	n=0
	while [ -e "$ROOT$manifest" ]; do
		n=$((n + 1))
		manifest=$BACKUP_DIR/$(stamp)-$n
	done
	mkdir -p "$ROOT$manifest" || fail "cannot create $manifest"
	: >"$ROOT$manifest/manifest"
}
record() { printf '%s\n' "$*" >>"$ROOT$manifest/manifest"; }
# create_file PATH: content on stdin; refuses to overwrite a different file
create_file() {
	tmp=$ROOT$1.nanokvm-new
	mkdir -p "$(dirname "$ROOT$1")" || fail "cannot create $(dirname "$1")"
	cat >"$tmp" || fail "cannot write $1"
	if [ -e "$ROOT$1" ]; then
		if cmp -s "$tmp" "$ROOT$1"; then
			rm -f "$tmp"
			return 1
		fi
		cp -p "$ROOT$1" "$ROOT$manifest/$(basename "$1").before" || fail "cannot back up $1"
		record "replaced $1"
	else
		record "created $1"
	fi
	chmod 0644 "$tmp"
	mv "$tmp" "$ROOT$1" || fail "cannot install $1"
	return 0
}
append_line() {
	cp -p "$ROOT$1" "$ROOT$manifest/$(basename "$1").before" || fail "cannot back up $1"
	record "appended $1"
	printf '%s\n' "$2" >>"$ROOT$1" || fail "cannot append to $1"
}
run() {
	say "run:     $*"
	"$@" 2>&1 | sed 's/^/         /'
}
# sysvinit and busybox init both reread inittab on SIGHUP ("init q" is only
# the sysvinit spelling of it).
reread_inittab() { run kill -HUP 1; }
# exit code for a refusal: 3 when the system is not handled, else 2
refusal_exit() {
	case $init in
	unknown | runit | openrc) exit 3 ;;
	*) exit 2 ;;
	esac
}

udev_rule() {
	cat <<EOF
# NanoKVM USB serial console, written by nanokvm-serial-console.sh $VERSION.
# Starts a login (serial-getty@<port>) on the NanoKVM's CDC ACM port when it
# appears, and keeps ModemManager from probing it (the device-level and the
# port-level mark, for old and new ModemManager). Remove with
# "nanokvm-serial-console.sh undo", or delete this file and reload udev.
SUBSYSTEM=="usb", ATTR{idVendor}=="$VID", ATTR{idProduct}=="$PID", ATTR{serial}=="$serial", ENV{ID_MM_DEVICE_IGNORE}="1"
ACTION=="add", SUBSYSTEM=="tty", KERNEL=="ttyACM[0-9]*", ATTRS{idVendor}=="$VID", ATTRS{idProduct}=="$PID", ATTRS{serial}=="$serial", ENV{ID_MM_DEVICE_IGNORE}="1", ENV{ID_MM_PORT_IGNORE}="1", TAG+="systemd", ENV{SYSTEMD_WANTS}+="serial-getty@%k.service", SYMLINK+="nanokvm-console"
EOF
}

apply_systemd() {
	[ -n "$serial" ] || fail "the NanoKVM is not connected and no --serial was given; apply needs its serial number to bind the rule to it"
	if udev_rule | create_file "$RULE_FILE"; then
		ok "wrote $RULE_FILE"
	else
		ok "$RULE_FILE already has this content"
	fi
	[ "$selinux" = 1 ] && have restorecon && run restorecon "$ROOT$RULE_FILE"
	if have udevadm; then
		run udevadm control --reload
		if [ -n "$dev_tty" ]; then
			# match flags, not a device path: old udevadm has no positional argument
			run udevadm trigger --subsystem-match=tty "--sysname-match=$dev_tty" --action=add
			sleep 1
			if have systemctl && systemctl is-active --quiet "serial-getty@$dev_tty.service"; then
				ok "serial-getty@$dev_tty.service is running: open the USB serial console on the NanoKVM and press Enter"
			else
				warn "serial-getty@$dev_tty.service is not active yet; check: systemctl status serial-getty@$dev_tty.service"
			fi
		else
			info "the login starts when the NanoKVM's port appears"
		fi
	else
		warn "udevadm not found; the rule takes effect after a reboot or a udev reload"
	fi
}

apply_inittab() {
	tty=${dev_tty:-ttyACM0}
	if [ -n "$inittab_present" ]; then
		ok "/etc/inittab already has the console line"
	else
		cmd="${agetty:-$getty} -L $TTY_BAUD $tty $TTY_TERM"
		# busybox init: tty::action:process. sysvinit: id:runlevels:action:process
		# with an id of up to four characters.
		if [ -n "$init_busybox" ]; then
			line="$tty::respawn:$cmd $INITTAB_TAG"
		else
			line="nkac:2345:respawn:$cmd $INITTAB_TAG"
		fi
		append_line /etc/inittab "$line"
		ok "appended to /etc/inittab: $line"
		[ -z "$ROOT" ] && reread_inittab
	fi
	[ -n "$dev_tty" ] || warn "init respawns the getty while /dev/$tty is absent and may pause it (\"respawning too fast\"); it recovers when the NanoKVM is connected"
}

do_apply() {
	report
	if [ "$refusals" -gt 0 ]; then
		manual_steps
		refusal_exit
	fi
	if [ -n "$foreign_rule" ] && [ -z "$rule_present" ] && [ "$init" = systemd ]; then
		say "nothing to do: the login is set up by $foreign_rule; this script adds no second rule (remove that one first if this script should own the setup)"
		exit 0
	fi
	if [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
		fail "apply needs root (sudo)"
	fi
	planned_changes
	if [ "$assume_yes" != 1 ]; then
		if [ -t 0 ]; then
			printf 'Apply these changes? [y/N] '
			read -r answer
			case $answer in y | Y | yes | YES) ;; *)
				say "nothing changed"
				exit 0
				;;
			esac
		else
			say "not a terminal: pass --yes to apply without the prompt; nothing changed"
			exit 0
		fi
	fi
	start_manifest
	case $init in
	systemd) apply_systemd ;;
	inittab) apply_inittab ;;
	esac
	if grep -qE '^(created|replaced|appended) ' "$ROOT$manifest/manifest"; then
		record "serial $serial"
		record "version $VERSION"
		ok "recorded in $manifest/manifest"
	else
		# nothing changed: no record, so undo reverts the previous real change
		rm -rf "$ROOT$manifest"
		say "nothing changed"
	fi
}

do_undo() {
	last=
	for d in "$ROOT$BACKUP_DIR"/*/; do
		[ -r "$d/manifest" ] && last=$d
	done
	if [ -z "$last" ]; then
		say "nothing to undo: no manifest in $BACKUP_DIR"
		exit 0
	fi
	if [ -z "$ROOT" ] && [ "$(id -u)" != 0 ]; then
		fail "undo needs root (sudo)"
	fi
	last=${last%/}
	say "undo:    $(basename "$last")"
	changed_udev=
	while read -r kind path; do
		case $kind in
		created)
			rm -f "$ROOT$path" && ok "removed $path"
			case $path in /etc/udev/*) changed_udev=yes ;; esac
			;;
		replaced | appended)
			cp -p "$last/$(basename "$path").before" "$ROOT$path" && ok "restored $path"
			case $path in
			/etc/udev/*) changed_udev=yes ;;
			/etc/inittab) [ -z "$ROOT" ] && reread_inittab ;;
			esac
			;;
		esac
	done <"$last/manifest"
	if [ -n "$changed_udev" ] && have udevadm; then
		run udevadm control --reload
		if [ -n "$dev_tty" ] && have systemctl; then
			run systemctl stop "serial-getty@$dev_tty.service"
		fi
	fi
	mv "$last/manifest" "$last/manifest.undone"
	ok "done; the backups stay in $last"
}

case $action in
plan)
	report
	if [ "$refusals" -gt 0 ]; then
		manual_steps
		refusal_exit
	fi
	if [ -n "$rule_present" ] && [ "$init" = systemd ] && { [ -z "$serial" ] || [ "$rule_serial" = "$serial" ]; }; then
		say "nothing to do: already set up"
		exit 0
	fi
	if [ -n "$foreign_rule" ] && [ "$init" = systemd ]; then
		say "nothing to do: already set up by $foreign_rule"
		exit 0
	fi
	if [ -n "$inittab_present" ] && [ "$init" = inittab ]; then
		say "nothing to do: already set up"
		exit 0
	fi
	planned_changes
	say "run \"$(basename "$0") apply\" as root to make these changes"
	;;
status)
	report
	[ "$refusals" -gt 0 ] && exit 2
	exit 0
	;;
apply) do_apply ;;
undo) do_undo ;;
esac
