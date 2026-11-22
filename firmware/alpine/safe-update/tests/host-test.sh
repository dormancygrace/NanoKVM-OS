#!/bin/sh
# Host tests for nkos-safe-update's failure paths (no device, no root). Run: sh tests/host-test.sh
# The tool is sourced with NKOS_SU_LIB=1. mount, reboot, rc-service, pgrep and setsid are stubs;
# /boot, the state and the run directories are temporary. The guard is driven one step at a time
# with an explicit uptime, so nothing depends on timing.
# shellcheck disable=SC2016,SC2034,SC2015,SC2115 # checks are eval'd strings that read these variables; T is never empty
set -u
HERE=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin"
for c in rc-service pgrep pkill logger; do printf '#!/bin/sh\nexit 1\n' > "$T/bin/$c"; done
printf '#!/bin/sh\necho "$*" >> "%s/setsid.calls"\n' "$T" > "$T/bin/setsid"
# mount: note whether the kernel-trial intent is on the root at the commit, then (unless told
# otherwise) clear the checkpoint flag the way a successful checkpoint=enable does
cat > "$T/bin/mount" <<EOF
#!/bin/sh
echo "\$*" >> "$T/mount.calls"
case "\$*" in *checkpoint=enable*)
	grep -q '^promote=1' "$T/state/ktrial" 2> /dev/null && echo yes > "$T/promote-before-commit"
	[ -e "$T/mount.keeps-disabled" ] || rm -f "$T/cp.disabled" ;;
esac
EOF
chmod 755 "$T/bin"/*
export PATH="$T/bin:$PATH" NKOS_SU_LIB=1 NKOS_SU_STATE="$T/state" NKOS_SU_RUN="$T/run" NKOS_SU_BOOT="$T/boot" \
	NKOS_SU_KT="$T/kt" NKOS_SU_LOCK="$T/lock" NKOS_SU_WDT="$T/wdt" NKOS_SU_ACTIVATE="$T/activate" NKOS_SU_WATCHER_WAIT=0
# shellcheck source=/dev/null
. "$HERE/nkos-safe-update"
export LIB="$HERE"
NOW=1000
# up(): $NOW, or, when $T/clock lists values, the next one on each call (time moving between steps)
up() {
	if [ -s "$T/clock" ]; then head -1 "$T/clock"; [ "$(wc -l < "$T/clock")" -gt 1 ] && sed -i 1d "$T/clock"; else echo "$NOW"; fi
}
cp_disabled() { [ -e "$T/cp.disabled" ]; }
# oncard.bad: the next read-back of the card returns garbage (once)
oncard() { if [ -e "$T/oncard.bad" ]; then rm -f "$T/oncard.bad"; echo bad; else sha256sum < "$1" | cut -d ' ' -f 1; fi; }
boot_id() { echo test-boot; }
feed() { echo "$1" >> "$T/feed.calls"; }
do_reboot() { echo reboot >> "$T/reboot.calls"; }
disarm() { echo disarm >> "$T/disarm.calls"; }
sync() { :; }

fails=0
check() { if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; fails=$((fails + 1)); fi; }
calls() { if [ -e "$T/$1.calls" ]; then wc -l < "$T/$1.calls" | tr -d ' '; else echo 0; fi; }
reset() {
	rm -rf "$T/run" "$T/kt" "$T/state" "$T/boot" "$T"/*.calls "$T/promote-before-commit" "$T/cp.disabled" \
		"$T/mount.keeps-disabled" "$T/oncard.bad" "$T/clock" "$T/states"
	mkdir -p "$T/run" "$T/kt" "$T/state" "$T/boot"
	NOW=1000 own=1 rebooting=
}
activate_ok() { printf '#!/bin/sh\necho good > "%s/boot/boot.sd"; sha256sum < "%s/boot/boot.sd" | cut -d " " -f 1 > "%s/boot/boot.sd.sha256"\n' "$T" "$T" "$T" > "$T/activate"; chmod 755 "$T/activate"; }
activate_fail() { printf '#!/bin/sh\nexit 1\n' > "$T/activate"; chmod 755 "$T/activate"; }
activate_badwrite() { printf '#!/bin/sh\necho good > "%s/boot/boot.sd"; echo 0000 > "%s/boot/boot.sd.sha256"\n' "$T" "$T" > "$T/activate"; chmod 755 "$T/activate"; }
trial() { touch "$T/cp.disabled"; printf 'id=%s\nboot=test-boot\n' "$1" > "$T/state/pending"; echo "id=$1" > "$T/state/applied"; }
ktrial() { echo "$1" > "$T/kt/id"; printf 'id=%s\npayload=x\n' "$1" > "$T/state/ktrial"; echo "$1" > "$T/boot/nkos-ktrial.running"; }

# --- decisions: published complete, first wins, only the winner may change it ----------------------
reset
check "a decision is published with its time" 'decide confirm 950 && [ "$(decision)" = confirm ] && [ "$(decision 2)" = 950 ]'
check "a second decision loses" '! decide rollback && [ "$(decision)" = confirm ]'
check "the winner can hand over to rollback" 'redecide rollback && [ "$(decision)" = rollback ]'
reset; echo "confi" > "$T/run/decision.4242"         # an interrupted claim: a private file only
check "an interrupted claim is not a decision" '[ -z "$(decision)" ] && decide rollback && [ "$(decision)" = rollback ]'

# --- guard_step: one round at a given uptime --------------------------------------------------------
reset; touch "$T/cp.disabled"; echo 2000 > "$T/run/deadline"
guard_step 1500; r=$?
check "before the deadline: feed up to the deadline" '[ "$r" = 0 ] && [ "$(cat "$T/feed.calls")" = 2000 ] && [ "$(calls reboot)" = 0 ]'
reset; touch "$T/cp.disabled"; echo 2000 > "$T/run/deadline"
guard_step 2000
check "at the deadline: rollback is decided and the board reboots once" '[ "$(decision)" = rollback ] && [ "$(calls reboot)" = 1 ]'
guard_step 2010
check "while rebooting: no second reboot, feed until reboot+GRACE" '[ "$(calls reboot)" = 1 ] && [ "$(tail -1 "$T/feed.calls")" = $((2000 + GRACE)) ]'
n=$(calls feed); guard_step $((2000 + GRACE))
check "a shutdown that hangs past GRACE: feeding stops" '[ "$(calls feed)" = "$n" ]'
reset; touch "$T/cp.disabled"; echo 2000 > "$T/run/deadline"; decide confirm 1990
guard_step 2005
check "deadline passed after a won confirm: no rollback, feed for the confirm" '[ "$(calls reboot)" = 0 ] && [ "$(decision)" = confirm ] && [ "$(cat "$T/feed.calls")" = $((1990 + CONFIRM_MAX)) ]'
n=$(calls feed); guard_step $((1990 + CONFIRM_MAX))
check "a confirm that stalls past CONFIRM_MAX: feeding stops, no reboot" '[ "$(calls feed)" = "$n" ] && [ "$(cat "$T/run/guard.state")" = stalled ] && [ "$(calls reboot)" = 0 ]'
touch "$T/run/confirmed"; guard_step $((1990 + CONFIRM_MAX + 5)); r=$?
check "a finished confirm: disarm and stop" '[ "$r" = 1 ] && [ "$(calls disarm)" = 1 ] && [ "$(cat "$T/run/guard.state")" = disarmed ]'
reset; echo 2000 > "$T/run/deadline"                  # checkpointing is on: committed behind our back
printf 'id=x\nboot=test-boot\n' > "$T/state/pending"
guard_step 1500; r=$?
check "checkpointing re-enabled by someone else: warning, disarm, stop" '[ "$r" = 1 ] && [ "$(calls disarm)" = 1 ] && grep -q WARNING "$T/state/history"'

# --- guard_setup: the states it publishes on success never include failed --------------------------------
reset; : > "$T/wdt"
( put() { echo "$2" >> "$T/states"; printf '%s\n' "$2" > "$RUN/$1"; }; guard_setup ) > /dev/null 2>&1
check "a successful setup never publishes failed" '! grep -q failed "$T/states" && [ "$(tail -1 "$T/states")" = "armed watchdog" ]'

# --- guard_setup: the watcher is trusted only once it reports armed -----------------------------------
reset; : > "$T/wdt"; echo k0 > "$T/kt/id"; echo id=k0 > "$T/state/ktrial"
( guard_setup && cat "$T/run/guard.state" ) > "$T/out" 2>&1
check "a watcher that never arms: the guard takes the watchdog itself" '[ "$(cat "$T/out")" = "armed watchdog" ]'
reset; echo k0 > "$T/kt/id"; echo id=k0 > "$T/state/ktrial"; echo armed > "$T/kt/watcher"
( guard_setup && cat "$T/run/guard.state" ) > "$T/out" 2>&1
check "an armed watcher: the guard leaves the watchdog to it" '[ "$(cat "$T/out")" = "armed watcher" ]'

# --- confirm ------------------------------------------------------------------------------------------
reset; trial t1; echo 99 > "$T/run/deadline"; NOW=100
( cmd_confirm ) > /dev/null 2>&1; r=$?
check "confirm after the deadline (before the guard notices) is refused" '[ "$r" != 0 ] && [ -z "$(decision)" ] && [ "$(calls mount)" = 0 ]'
reset; trial t1; echo 2000 > "$T/run/deadline"; decide rollback 1500
( cmd_confirm ) > /dev/null 2>&1; r=$?
check "confirm after a rollback decision is refused" '[ "$r" != 0 ] && [ "$(decision)" = rollback ] && [ "$(calls mount)" = 0 ]'
reset; trial t1; echo 100 > "$T/run/deadline"; printf '99\n100\n' > "$T/clock"   # the deadline passes while claiming
( cmd_confirm ) > /dev/null 2>&1; r=$?
check "a deadline that passes while the claim is published: no commit, handed to rollback" '[ "$r" != 0 ] && [ "$(decision)" = rollback ] && ! grep -q checkpoint=enable "$T/mount.calls" 2> /dev/null'
reset; trial t2; ktrial k1; echo 2000 > "$T/run/deadline"; chmod 555 "$T/state"
( cmd_confirm ) > /dev/null 2>&1; r=$?; chmod 755 "$T/state"
check "an intent that cannot be written: nothing claimed, nothing committed" '[ "$r" != 0 ] && [ -z "$(decision)" ] && [ "$(calls mount)" = 0 ]'
reset; trial t3; echo 2000 > "$T/run/deadline"; touch "$T/mount.keeps-disabled"
( cmd_confirm ) > /dev/null 2>&1; r=$?
check "a remount that leaves checkpointing disabled hands over to rollback" '[ "$r" != 0 ] && [ "$(decision)" = rollback ]'
reset; trial t4; ktrial k2; echo 2000 > "$T/run/deadline"; activate_fail; echo disarmed > "$T/run/guard.state"
( cmd_confirm ) > "$T/out" 2>&1; r=$?
check "promote=1 is on the root before checkpointing is re-enabled" '[ -e "$T/promote-before-commit" ]'
check "a failed boot-image install makes confirm fail, and the retry state is kept" '[ "$r" != 0 ] && grep -q "BOOT IMAGE NOT INSTALLED" "$T/out" && [ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.running" ]'
activate_ok; ( cmd_kernel_finish ) > /dev/null 2>&1
check "kernel-finish (root committed) installs the boot image and clears the state" '[ ! -f "$T/state/ktrial" ] && [ ! -e "$T/boot/nkos-ktrial.running" ]'
reset; trial t5; ktrial k3; echo promote=1 >> "$T/state/ktrial"; activate_ok
( cmd_kernel_finish ) > /dev/null 2>&1; r=$?
check "kernel-finish during an uncommitted trial is refused, even with promote=1" '[ "$r" != 0 ] && [ -f "$T/state/ktrial" ] && [ ! -e "$T/boot/boot.sd" ]'

# --- boot entry ---------------------------------------------------------------------------------------
for mode in fail badwrite ok; do
	reset; rm -rf "$T/kt"; activate_$mode
	printf 'id=k4\npayload=x\n' > "$T/state/ktrial"; echo k4 > "$T/boot/nkos-ktrial.running"; echo trial > "$T/boot/nkos-ktrial.sd"
	( cmd_boot ) > /dev/null 2>&1; r=$?
	if [ "$mode" = ok ]; then
		check "boot, restore ($mode): records removed, service succeeds" '[ "$r" = 0 ] && [ ! -f "$T/state/ktrial" ] && [ ! -e "$T/boot/nkos-ktrial.running" ]'
	else
		check "boot, restore ($mode): records kept, service fails" '[ "$r" != 0 ] && [ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.running" ]'
	fi
done
reset; trial t6; echo 2000 > "$T/run/deadline"
( cmd_boot ) > /dev/null 2>&1
check "a service restart during this boot's trial leaves it alone" '[ -e "$T/state/pending" ] && [ -e "$T/state/applied" ] && [ ! -e "$T/state/history" ]'
reset; ktrial k5; echo 2000 > "$T/run/deadline"
( cmd_boot ) > /dev/null 2>&1; echo 2000 > "$T/run/deadline"; ( cmd_boot ) > /dev/null 2>&1
check "the trial boot is claimed once: one worker, the run directory kept" '[ "$(calls setsid)" = 1 ] && [ "$(cat "$T/run/deadline")" = 2000 ]'
reset; rm -rf "$T/kt"; activate_ok; printf 'id=k10\npayload=x\narmed_boot=earlier\n' > "$T/state/ktrial"; echo k10 > "$T/boot/nkos-ktrial.armed"
exec 7>> "$T/lock"; flock -n 7                       # another process (kernel-trial arming) holds the lock
( cmd_boot ) > /dev/null 2>&1; r=$?
exec 7>&-
check "recovery while the update lock is held: skipped, records kept" '[ "$r" = 0 ] && [ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.armed" ] && [ ! -e "$T/boot/boot.sd" ]'
reset; rm -rf "$T/kt"; activate_ok; printf 'id=k11\npayload=x\narmed_boot=test-boot\n' > "$T/state/ktrial"; echo k11 > "$T/boot/nkos-ktrial.armed"
( cmd_boot ) > /dev/null 2>&1; r=$?
check "a trial armed in this boot (awaiting its reboot) is not recovered" '[ "$r" = 0 ] && [ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.armed" ]'
( cmd_kernel_finish ) > /dev/null 2>&1; r=$?
check "kernel-finish refuses a trial awaiting its reboot" '[ "$r" != 0 ] && [ -e "$T/boot/nkos-ktrial.armed" ]'
mv "$T/boot/nkos-ktrial.armed" "$T/boot/nkos-ktrial.failed"
( cmd_boot ) > /dev/null 2>&1
check "a trial whose arming failed in this boot is recovered" '[ ! -f "$T/state/ktrial" ] && [ ! -e "$T/boot/nkos-ktrial.failed" ] && [ -f "$T/boot/boot.sd" ]'
reset; printf 'id=t7\nboot=earlier-boot\n' > "$T/state/pending"
( cmd_boot ) > /dev/null 2>&1
check "a trial from an earlier boot is recorded as rolled back" '[ ! -e "$T/state/pending" ] && grep -q "rolled back t7" "$T/state/history"'

# --- arming: the final install step ----------------------------------------------------------------------
reset; activate_ok; printf 'id=k6\npayload=x\n' > "$T/state/ktrial"; echo k6 > "$T/boot/nkos-ktrial.armed"; echo trial > "$T/boot/boot.sd.new"; touch "$T/oncard.bad"
( kt_install_image "$(sha256sum < "$T/boot/boot.sd.new" | cut -d ' ' -f 1)" ) > /dev/null 2>&1; r=$?
check "a failed final install: nothing stays armed; restored and verified, records removed" '[ "$r" != 0 ] && [ ! -e "$T/boot/nkos-ktrial.armed" ] && [ ! -e "$T/boot/nkos-ktrial.failed" ] && [ ! -f "$T/state/ktrial" ] && [ -f "$T/boot/boot.sd" ]'
reset; activate_fail; printf 'id=k7\npayload=x\n' > "$T/state/ktrial"; echo k7 > "$T/boot/nkos-ktrial.armed"; echo trial > "$T/boot/boot.sd.new"; touch "$T/oncard.bad"
( kt_install_image x ) > "$T/out" 2>&1; r=$?
check "a failed final install whose restore fails: records kept, marker disarmed" '[ "$r" != 0 ] && [ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.failed" ] && [ ! -e "$T/boot/nkos-ktrial.armed" ] && grep -q "could not be restored" "$T/out"'

# --- the trial init: anchors and blocks exactly once --------------------------------------------------
printf '#!/busybox sh\nmount -o ro /dev/mmcblk0p1 /boot\numount /boot\nexec switch_root /realroot /sbin/init\n' > "$T/init.ok"
check "a stock-shaped init gets both blocks" 'kt_patch_init "$T/init.ok" "$T/init.new" 2> /dev/null'
printf '#!/busybox sh\numount /boot\nexec  switch_root /realroot /sbin/init\n' > "$T/init.ws"
check "a different switch_root line is refused" '! kt_patch_init "$T/init.ws" "$T/init.new2" 2> /dev/null'
printf '#!/busybox sh\numount /boot\numount /boot\nexec switch_root /realroot /sbin/init\n' > "$T/init.twice"
check "a duplicated anchor is refused" '! kt_patch_init "$T/init.twice" "$T/init.new3" 2> /dev/null'

# --- the kernel trial record must be written before /boot changes ----------------------------------------
reset
check "the record is written and read back" 'kt_record k8 600 "upgrade" rel abc && [ "$(get id "$KSTATE")" = k8 ]'
rm -rf "$T/state"; touch "$T/state"                 # STATE is not a directory: the write fails
check "a failed record write is reported" '! kt_record k9 600 "upgrade" rel abc 2> /dev/null'
rm -f "$T/state"

echo
if [ "$fails" = 0 ]; then echo "all host tests passed"; else echo "$fails host test(s) failed"; exit 1; fi
