#!/bin/bash
# run.sh [BUILD_DIR]: builds the security-boundary tests with the host g++ against the real server-cpp sources and
# runs them. glaze, rapidyaml and libwebsockets are the versions pinned in platform/sources.lock, downloaded once into
# BUILD_DIR and checked against their sha256; libwebsockets is configured as server-cpp/build-libwebsockets.sh does,
# for the host. Each test links only the units it reaches (--gc-sections), no device library.
# Needs g++ with C++23, cmake, ninja, OpenSSL 3.2 or later (headers), libcrypt, curl, patch, openssl and ssh-keygen.
set -euo pipefail
tests=$(cd "$(dirname "$0")" && pwd)
src=$tests/../src
lock=$tests/../../platform/sources.lock
b=${1:-${TMPDIR:-/tmp}/nkos-server-tests}
mkdir -p "$b/dl" "$b/obj" "$b/inc"

# The pinned download of name, checked.
fetch() {
	local url sum file
	read -r url sum < <(awk -v n="$1" '$1 == n { print $2, $3 }' "$lock")
	[ -n "$sum" ] || { echo "$1: not in sources.lock" >&2; return 1; }
	file=$b/dl/$1-${url##*/}
	[ -f "$file" ] || { curl -fsSL --retry 3 -o "$file.part" "$url" && mv "$file.part" "$file"; } || return 1
	echo "$sum  $file" | sha256sum -c --quiet - >&2 || return 1
	echo "$file"
}

glaze=$(fetch glaze)
lws=$(fetch libwebsockets)
ryml=$(fetch rapidyaml)
cp "$ryml" "$b/inc/ryml_all.hpp"
if [ ! -f "$b/glaze.stamp" ] || [ "$(cat "$b/glaze.stamp")" != "$glaze" ]; then
	rm -rf "$b/glaze" && mkdir "$b/glaze" && tar -xzf "$glaze" -C "$b/glaze" --strip-components=1
	echo "$glaze" > "$b/glaze.stamp"
fi
lws_patches=("$tests"/../patches/libwebsockets/*.patch)
lws_stamp="$lws $(cat "${lws_patches[@]}" | sha256sum)"
if [ ! -f "$b/lws.stamp" ] || [ "$(cat "$b/lws.stamp")" != "$lws_stamp" ]; then
	rm -rf "$b/lws-src" "$b/lws-build" "$b/lws" && mkdir "$b/lws-src"
	tar -xzf "$lws" -C "$b/lws-src" --strip-components=1
	for patch_file in "${lws_patches[@]}"; do patch -d "$b/lws-src" -p1 < "$patch_file"; done
	cmake -S "$b/lws-src" -B "$b/lws-build" -G Ninja -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$b/lws" \
		-DLWS_WITH_SSL=ON -DLWS_WITH_HTTP2=ON -DLWS_WITH_HTTP3=OFF -DLWS_WITH_RANGES=ON -DLWS_WITH_SHARED=OFF \
		-DLWS_WITH_STATIC=ON -DLWS_WITHOUT_TESTAPPS=ON -DLWS_WITHOUT_TEST_SERVER=ON -DLWS_WITHOUT_TEST_CLIENT=ON \
		-DLWS_WITH_MINIMAL_EXAMPLES=OFF -DLWS_WITH_ZLIB=OFF -DLWS_WITHOUT_EXTENSIONS=ON -DLWS_IPV6=ON \
		-DLWS_WITH_LIBUV=OFF -DLWS_WITH_LIBEVENT=OFF -DLWS_WITH_GLIB=OFF -DDISABLE_WERROR=ON > "$b/lws.log" 2>&1 \
		&& ninja -C "$b/lws-build" install >> "$b/lws.log" 2>&1 || { tail -30 "$b/lws.log" >&2; exit 1; }
	echo "$lws_stamp" > "$b/lws.stamp"
fi

# The server units the tests reach, as one archive.
units="api/multipart auth/brute_force auth/jwt auth/password auth/session auth/store auth/totp bmc/config bmc/ipmi
	bmc/redfish common/log common/loop common/process config/config config/yaml http/router network/util
	storage/download storage/go storage/userdisk system/ssh_keys system/tls_x509"
flags=(-std=c++23 -O1 -pthread -ffunction-sections -fdata-sections -I"$src" -I"$b/glaze/include" -I"$b/inc"
	-I"$b/lws/include" -I"$tests")
export b src
printf '%s\n' $units | xargs -P "$(nproc)" -I{} sh -c 'o="$b/obj/$(echo {} | tr / _).o"; g++ "$@" -c "$src/{}.cpp" -o "$o"' _ "${flags[@]}"
rm -f "$b/libserver.a" && ar rcs "$b/libserver.a" "$b"/obj/*.o

# Keys and certificates from independent tools.
f=$b/fixtures
rm -rf "$f" && mkdir "$f"
for k in ed25519:256 ecdsa:256 ecdsa:384 ecdsa:521 rsa:1024 rsa:2048; do
	ssh-keygen -q -N '' -t "${k%:*}" -b "${k#*:}" -C "laptop" -f "$f/${k/:/}"
	ssh-keygen -l -E sha256 -f "$f/${k/:/}.pub" | awk '{ print $2 }' > "$f/${k/:/}.fp"
done
ssh-keygen -q -N '' -t ed25519 -C "second" -f "$f/other"
for name in leaf issuer other; do
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj "/CN=$name.example" -days 365 \
		-keyout "$f/$name.key" -out "$f/$name.crt" 2> /dev/null
done

status=0
for t in "$tests"/*_test.cpp; do
	t=$(basename "$t" .cpp)
	g++ "${flags[@]}" "$tests/$t.cpp" -o "$b/$t" -Wl,--gc-sections "$b/libserver.a" "$b/lws/lib/libwebsockets.a" \
		-lssl -lcrypto -lcrypt
	"$b/$t" "$f" || status=1
done
exit $status
