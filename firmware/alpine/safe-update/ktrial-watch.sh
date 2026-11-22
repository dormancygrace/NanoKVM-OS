# --- nkos-safe-update kernel trial: watchdog (inserted before "exec switch_root") ---
# A shell forked here outlives switch_root, which deletes this initramfs, so it
# uses only builtins. It feeds the hardware watchdog while userspace keeps its
# promises: check in within @BOOT_MAX@ s of boot, then finish before the
# deadline it writes (+@GRACE@ s for a graceful reboot). After that it stops
# feeding and the board resets onto boot.prev. A confirm makes it disarm.
if [ -n "$nkos_ktrial" ]; then
	busybox mkdir -p /dev/.nkos-ktrial && busybox mkfifo /dev/.nkos-ktrial/tick &&
		cat /nkos-ktrial.id > /dev/.nkos-ktrial/id
	nkos_watch() {
		exec 3>/dev/watchdog0 || { echo failed > /dev/.nkos-ktrial/watcher; exit 1; }
		echo armed > /dev/.nkos-ktrial/watcher
		limit=@BOOT_MAX@
		while :; do
			read -r up rest < /proc/uptime
			up=${up%%.*}
			if [ -e /dev/.nkos-ktrial/confirmed ]; then
				printf V >&3
				exec 3>&-
				echo disarmed > /dev/.nkos-ktrial/watcher
				exit 0
			fi
			if [ -e /dev/.nkos-ktrial/deadline ] && read -r l < /dev/.nkos-ktrial/deadline; then
				case "$l" in ''|*[!0-9]*) ;; *) limit=$((l + @GRACE@)) ;; esac
			fi
			if [ "$up" -lt "$limit" ]; then printf x >&3; else echo expired > /dev/.nkos-ktrial/watcher; fi
			read -t 5 x <> /dev/.nkos-ktrial/tick
		done
	}
	nkos_watch < /dev/null > /dev/null 2>&1 &
fi
# --- end of kernel trial watchdog ---
