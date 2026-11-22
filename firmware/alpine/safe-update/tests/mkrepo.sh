#!/bin/sh
# Build a local, unsigned test repository on the device with apk-tools 3 (mkpkg/mkndx).
#   nkos-sut-demo 1-r0 / 2-r0   v2 changes a binary-like file and a config, and adds a 40 MiB payload
#   nkos-sut-breaknet 1-r0      post-install takes the network down: "boots but unreachable"
set -eu
R=${1:-/data/nkos-sut-repo}
# Never delete a caller's directory: the destination must not exist yet (or be empty).
if [ -e "$R" ] && [ -n "$(ls -A "$R" 2> /dev/null)" ]; then
	echo "mkrepo.sh: $R exists and is not empty; choose a new directory" >&2; exit 1
fi
mkdir -p "$R"
W=$(mktemp -d /data/.nkos-sut-build.XXXXXX)
trap 'rm -rf "$W"' EXIT
mk() { # name version root [post-install script]
	set -- "$1" "$2" "$3" "${4:-}"
	if [ -n "$4" ]; then
		apk mkpkg -I "name:$1" -I "version:$2" -I "arch:noarch" -I "description:nkos-safe-update test" \
			-I "license:MIT" -F "$3" -s "post-install:$4" -o "$R/$1-$2.apk"
	else
		apk mkpkg -I "name:$1" -I "version:$2" -I "arch:noarch" -I "description:nkos-safe-update test" \
			-I "license:MIT" -F "$3" -o "$R/$1-$2.apk"
	fi
}
for v in 1 2; do
	d=$W/demo$v; mkdir -p "$d/usr/share/nkos-sut" "$d/etc"
	echo "demo $v" > "$d/usr/share/nkos-sut/version"
	echo "setting=$v" > "$d/etc/nkos-sut.conf"
	[ "$v" = 1 ] || head -c 41943040 /dev/urandom > "$d/usr/share/nkos-sut/payload"
	mk nkos-sut-demo "$v-r0" "$d"
done
d=$W/breaknet; mkdir -p "$d/usr/share/nkos-sut"; echo breaknet > "$d/usr/share/nkos-sut/breaknet"
cat > "$W/breaknet.post" <<'EOS'
#!/bin/sh
# Simulates an update that leaves the device running but unreachable.
( sleep 5; for i in $(ls /sys/class/net); do [ "$i" = lo ] || ip link set "$i" down; done ) >/dev/null 2>&1 &
exit 0
EOS
mk nkos-sut-breaknet 1-r0 "$d" "$W/breaknet.post"
apk mkndx --allow-untrusted -o "$R/Packages.adb" "$R"/*.apk
ls -la "$R"
