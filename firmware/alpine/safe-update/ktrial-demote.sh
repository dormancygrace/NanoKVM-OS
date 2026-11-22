# --- nkos-safe-update kernel trial: boot-once (inserted before "umount /boot") ---
# Only an image built by nkos-safe-update carries /nkos-ktrial.id, and only an
# armed marker with the same id starts a trial. Renaming boot.sd away first
# makes U-Boot boot boot.prev (the old kernel) on any reset from here on; the
# marker is consumed in the same step, so this image never trials twice.
nkos_ktrial=
if [ -f /nkos-ktrial.id ] && [ -f /boot/nkos-ktrial.armed ] &&
	[ "$(cat /boot/nkos-ktrial.armed)" = "$(cat /nkos-ktrial.id)" ]; then
	if mount -o remount,rw /boot; then
		busybox mv /boot/boot.sd /boot/nkos-ktrial.sd && sync &&
			busybox mv /boot/nkos-ktrial.armed /boot/nkos-ktrial.running && sync &&
			nkos_ktrial=1
		mount -o remount,ro /boot
	fi
fi
# --- end of kernel trial boot-once ---
