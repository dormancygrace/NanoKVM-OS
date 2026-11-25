#!/bin/bash
# build-libwebsockets.sh SOURCE OUTPUT TOOLCHAIN_FILE OPENSSL CFLAGS: libwebsockets (pinned in platform/sources.lock)
# as NanoKVM-Server uses it: a static library with TLS on our OpenSSL, HTTP/1.1, HTTP/2, WebSocket and byte ranges;
# no HTTP/3 (it would select GnuTLS), no test programs, no event-library back ends. OUTPUT gets include/ and lib/.
set -euo pipefail
src=$1 out=$2 toolchain=$3 ssl=$4 cflags=$5
build=$out.build
rm -rf "$build" "$out"
cmake -S "$src" -B "$build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$toolchain" \
  -DCMAKE_BUILD_TYPE=Release "-DCMAKE_C_FLAGS=$cflags" -DCMAKE_INSTALL_PREFIX="$out" \
  -DLWS_WITH_SSL=ON -DLWS_OPENSSL_INCLUDE_DIRS="$ssl/include" \
  "-DLWS_OPENSSL_LIBRARIES=$ssl/libssl.so;$ssl/libcrypto.so" \
  -DLWS_WITH_HTTP2=ON -DLWS_WITH_HTTP3=OFF -DLWS_WITH_RANGES=ON -DLWS_WITH_SHARED=OFF -DLWS_WITH_STATIC=ON \
  -DLWS_WITHOUT_TESTAPPS=ON -DLWS_WITHOUT_TEST_SERVER=ON -DLWS_WITHOUT_TEST_CLIENT=ON \
  -DLWS_WITH_MINIMAL_EXAMPLES=OFF -DLWS_WITH_ZLIB=OFF -DLWS_WITHOUT_EXTENSIONS=ON \
  -DLWS_IPV6=ON -DLWS_WITH_LIBUV=OFF -DLWS_WITH_LIBEVENT=OFF -DLWS_WITH_GLIB=OFF -DDISABLE_WERROR=ON > "$build.log" 2>&1 \
  || { tail -30 "$build.log" >&2; exit 1; }
ninja -C "$build" install >> "$build.log" 2>&1 || { grep -i error "$build.log" | head -20 >&2; exit 1; }
rm -rf "$build"
