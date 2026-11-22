#!/bin/sh
# Host tests for nkos-safe-update's failure paths (no device, no root). Run: sh tests/host-test.sh
# The tool is sourced with NKOS_SU_LIB=1; mount, reboot, rc-service and pgrep are stubs, and
# /boot, the state and the run directories are temporary.
# shellcheck disable=SC2016,SC2034,SC2015,SC2115 # checks are eval'd strings that read these variables; T is never empty
set -u
HERE=$(cd "$(dirname "$0")/.." && pwd)
T=$(mktemp -d)
trap 'kill $(jobs -p) 2> /dev/null; rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/boot" "$T/state" "$T/run" "$T/kt" "$T/lib"
for c in rc-service pgrep pkill logger; do printf '#!/bin/sh\nexit 1\n' > "$T/bin/$c"; done
printf '#!/bin/sh\necho "$*" >> "%s/reboot.calls"\n' "$T" > "$T/bin/reboot"
# mount: record whether the kernel-trial intent was already written, then flip the checkpoint flag
cat > "$T/bin/mount" <<EOF
#!/bin/sh
case "\$*" in *checkpoint=enable*) grep -q '^promote=1' "$T/state/ktrial" 2> /dev/null && echo yes > "$T/promote-before-commit"; rm -f "$T/cp.disabled" ;; esac
EOF
chmod 755 "$T/bin"/*
export PATH="$T/bin:$PATH" NKOS_SU_LIB=1 NKOS_SU_STATE="$T/state" NKOS_SU_RUN="$T/run" NKOS_SU_BOOT="$T/boot" \
	NKOS_SU_KT="$T/kt" NKOS_SU_LOCK="$T/lock" NKOS_SU_WDT="$T/wdt" NKOS_SU_FEED=1 NKOS_SU_ACTIVATE="$T/activate"
# shellcheck source=/dev/null
. "$HERE/nkos-safe-update"
export LIB="$HERE"                                  # the fragments next to the script
cp_disabled() { [ -e "$T/cp.disabled" ]; }
oncard() { sha256sum < "$1" | cut -d ' ' -f 1; }
boot_id() { echo test-boot; }

fails=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fails=$((fails + 1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
reset_run() { rm -rf "${T:?}/run" "$T/kt" "$T/state" "$T/boot" "$T/reboot.calls" "$T/promote-before-commit"; mkdir -p "$T/run" "$T/kt" "$T/state" "$T/boot"; }
activate_ok() { printf '#!/bin/sh\necho good > "%s/boot/boot.sd"; sha256sum < "%s/boot/boot.sd" | cut -d " " -f 1 > "%s/boot/boot.sd.sha256"\n' "$T" "$T" "$T" > "$T/activate"; chmod 755 "$T/activate"; }
activate_fail() { printf '#!/bin/sh\nexit 1\n' > "$T/activate"; chmod 755 "$T/activate"; }
activate_badwrite() { printf '#!/bin/sh\necho good > "%s/boot/boot.sd"; echo 0000 > "%s/boot/boot.sd.sha256"\n' "$T" "$T" > "$T/activate"; chmod 755 "$T/activate"; }

# --- confirm vs rollback are decided once -------------------------------------------------------
reset_run
decide confirm; check "the first decision wins" '[ "$(decision)" = confirm ]'
check "a later rollback loses" '! decide rollback'
check "the decision is unchanged" '[ "$(decision)" = confirm ]'

# --- the guard: a won confirm that never finishes stops the feeding ------------------------------
reset_run; touch "$T/cp.disabled"; : > "$T/wdt"; echo $(( $(up) + 10000 )) > "$T/run/deadline"
mkdir "$T/run/decision"; echo "confirm $(( $(up) - CONFIRM_MAX - 5 ))" > "$T/run/decision/what"
( guard ) > /dev/null 2>&1 & g=$!
sleep 3; s1=$(wc -c < "$T/wdt"); sleep 3; s2=$(wc -c < "$T/wdt"); kill "$g" 2> /dev/null; wait "$g" 2> /dev/null
check "a stalled confirm stops feeding the watchdog" '[ "$s1" = "$s2" ] && [ "$(cat "$T/run/guard.state")" = stalled ]'
check "a stalled confirm does not reboot (the watchdog does)" '[ ! -e "$T/reboot.calls" ]'

# --- the guard: deadline passed while a confirm is running -> no reboot ---------------------------
reset_run; touch "$T/cp.disabled"; : > "$T/wdt"; echo 1 > "$T/run/deadline"
mkdir "$T/run/decision"; echo "confirm $(up)" > "$T/run/decision/what"
( guard ) > /dev/null 2>&1 & g=$!
sleep 3; kill "$g" 2> /dev/null; wait "$g" 2> /dev/null
check "a deadline after a won confirm does not roll back" '[ ! -e "$T/reboot.calls" ] && [ -s "$T/wdt" ]'

# --- the guard in a kernel trial trusts the watcher only once it reports "armed" ----------------------
reset_run; touch "$T/cp.disabled"; : > "$T/wdt"; echo $(( $(up) + 10000 )) > "$T/run/deadline"
echo k0 > "$T/kt/id"; echo id=k0 > "$T/state/ktrial"            # watcher never reports armed
( guard ) > /dev/null 2>&1 & g=$!
sleep 20; kill "$g" 2> /dev/null; wait "$g" 2> /dev/null
check "a watcher that never arms: the guard takes the watchdog itself" '[ "$(cat "$T/run/guard.state")" = "armed watchdog" ] && [ -s "$T/wdt" ]'
reset_run; touch "$T/cp.disabled"; : > "$T/wdt"; echo $(( $(up) + 10000 )) > "$T/run/deadline"
echo k0 > "$T/kt/id"; echo id=k0 > "$T/state/ktrial"; echo armed > "$T/kt/watcher"
( guard ) > /dev/null 2>&1 & g=$!
sleep 3; kill "$g" 2> /dev/null; wait "$g" 2> /dev/null
check "an armed watcher: the guard leaves the watchdog to it" '[ "$(cat "$T/run/guard.state")" = "armed watcher" ] && [ ! -s "$T/wdt" ] && [ "$(cat "$T/kt/deadline")" = "$(cat "$T/run/deadline")" ]'

# --- the guard: deadline passed, nobody decided -> rollback wins, a late confirm loses -------------
reset_run; touch "$T/cp.disabled"; : > "$T/wdt"; echo 1 > "$T/run/deadline"
( guard ) > /dev/null 2>&1 & g=$!
sleep 3; kill "$g" 2> /dev/null; wait "$g" 2> /dev/null
check "the deadline decides rollback and reboots" '[ "$(decision)" = rollback ] && [ -s "$T/reboot.calls" ]'
printf 'id=t1\nboot=test-boot\n' > "$T/state/pending"; echo id=t1 > "$T/state/applied"
check "confirm after the rollback decision is refused" '! (cmd_confirm) > /dev/null 2>&1 && [ -e "$T/cp.disabled" ]'

# --- confirm in a kernel trial: intent written before the commit; failed promotion propagates -------
reset_run; touch "$T/cp.disabled"; activate_fail
echo k1 > "$T/kt/id"; printf 'id=k1\npayload=x\n' > "$T/state/ktrial"; echo k1 > "$T/boot/nkos-ktrial.running"
printf 'id=t2\nboot=test-boot\n' > "$T/state/pending"; echo id=t2 > "$T/state/applied"
( cmd_confirm ) > "$T/out" 2>&1; rc=$?
check "promote=1 is on the root before checkpointing is re-enabled" '[ -e "$T/promote-before-commit" ]'
check "a failed boot-image install makes confirm fail" '[ "$rc" != 0 ] && grep -q "BOOT IMAGE NOT INSTALLED" "$T/out"'
check "the retry state is kept" '[ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.running" ]'
activate_ok
( cmd_kernel_finish ) > /dev/null 2>&1
check "kernel-finish installs the boot image and clears the state" '[ ! -f "$T/state/ktrial" ] && [ ! -e "$T/boot/nkos-ktrial.running" ]'

# --- rollback boot: records stay until the restored boot.sd is verified ----------------------------
for mode in fail badwrite ok; do
	reset_run; rm -rf "$T/kt"; activate_$mode
	printf 'id=k2\npayload=x\n' > "$T/state/ktrial"; echo k2 > "$T/boot/nkos-ktrial.running"; echo trial > "$T/boot/nkos-ktrial.sd"
	( kt_boot ) > /dev/null 2>&1
	if [ "$mode" = ok ]; then
		check "restore ($mode): records removed after a verified boot.sd" '[ ! -f "$T/state/ktrial" ] && [ ! -e "$T/boot/nkos-ktrial.running" ]'
	else
		check "restore ($mode): records kept for the next boot" '[ -f "$T/state/ktrial" ] && [ -e "$T/boot/nkos-ktrial.running" ]'
	fi
done

# --- the trial init: anchors and blocks exactly once ---------------------------------------------
printf '#!/busybox sh\nmount -o ro /dev/mmcblk0p1 /boot\numount /boot\nexec switch_root /realroot /sbin/init\n' > "$T/init.ok"
check "a stock-shaped init gets both blocks" 'kt_patch_init "$T/init.ok" "$T/init.new" 2> /dev/null'
printf '#!/busybox sh\numount /boot\nexec  switch_root /realroot /sbin/init\n' > "$T/init.ws"
check "a different switch_root line is refused" '! kt_patch_init "$T/init.ws" "$T/init.new2" 2> /dev/null'
printf '#!/busybox sh\numount /boot\numount /boot\nexec switch_root /realroot /sbin/init\n' > "$T/init.twice"
check "a duplicated anchor is refused" '! kt_patch_init "$T/init.twice" "$T/init.new3" 2> /dev/null'

# --- the kernel trial record must be written before /boot changes ---------------------------------
reset_run
check "the record is written and read back" 'kt_record k3 600 "upgrade" rel abc && [ "$(get id "$KSTATE")" = k3 ]'
rm -rf "$T/state"; touch "$T/state"                 # STATE is not a directory: the write fails
check "a failed record write is reported" '! kt_record k4 600 "upgrade" rel abc 2> /dev/null'
rm -f "$T/state"

echo
[ "$fails" = 0 ] && echo "all host tests passed" || { echo "$fails host test(s) failed"; exit 1; }
