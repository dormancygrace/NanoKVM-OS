#!/bin/bash
# build.sh SOURCES OUTPUT: NanoKVM-Server and its helpers from the unpacked inputs of platform/sources.lock.
#
# SOURCES holds: libwebsockets/, glaze/, rapidyaml.hpp, libdatachannel/ (with deps/libjuice, deps/libsrtp,
# deps/usrsctp, deps/plog filled in), libjpeg-turbo/, libspng/, live555/, curl/. The environment gives the rest:
#   CROSS     the toolchain prefix (.../riscv64-buildroot-linux-musl-)
#   CFLAGS    the server CPU profile (scripts/nanokvm_cpu_profile.py server: -O3)
#   OPENSSL   the nanokvm-openssl build (include/, libssl.so, libcrypto.so)
#   ZLIB      the zlib-ng build of the tools step (zlib.h, libz.so)
#   NATIVE    the native capture libraries (libkvm.so, libkvm_mmf.so)
#   CMAKE     cmake; ninja is used for every build
#   JOBS      parallel jobs
# OUTPUT gets NanoKVM-Server and the helpers, stripped, and the dependency builds below OUTPUT/deps.
set -euo pipefail
src=$(realpath "$1") out=$2
here=$(cd "$(dirname "$0")" && pwd)
: "${CROSS:?}" "${CFLAGS:?}" "${OPENSSL:?}" "${ZLIB:?}" "${NATIVE:?}" "${CMAKE:=cmake}" "${JOBS:=4}"
mkdir -p "$out/deps"
out=$(realpath "$out")
deps=$out/deps
log() { printf '== server: %s\n' "$*"; }

toolchain=$deps/toolchain.cmake
cat > "$toolchain" <<EOF
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR riscv64)
set(CMAKE_C_COMPILER ${CROSS}gcc)
set(CMAKE_CXX_COMPILER ${CROSS}g++)
set(CMAKE_SYSROOT $("${CROSS}gcc" -print-sysroot))
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
# The optimization level is the profile's, in CFLAGS: Release only adds NDEBUG (by default it appends -O3 after it).
set(CMAKE_C_FLAGS_RELEASE "-DNDEBUG" CACHE STRING "")
set(CMAKE_CXX_FLAGS_RELEASE "-DNDEBUG" CACHE STRING "")
EOF
# The patches of each library, in name order (libdatachannel's also patch its libjuice).
patches() {
    local dir=$1 p
    for p in "$here/patches/$2"/*.patch; do
        [ -f "$p" ] || continue
        patch -d "$dir" -p1 -s -N < "$p"
    done
}

log libwebsockets
patches "$src/libwebsockets" libwebsockets
PATH=$(dirname "$CMAKE"):$PATH bash "$here/build-libwebsockets.sh" "$src/libwebsockets" "$deps/lws" "$toolchain" "$OPENSSL" "$CFLAGS"

log libdatachannel
patches "$src/libdatachannel" libdatachannel
"$CMAKE" -S "$src/libdatachannel" -B "$deps/ldc.build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$toolchain" \
    -DCMAKE_BUILD_TYPE=Release "-DCMAKE_C_FLAGS=$CFLAGS" "-DCMAKE_CXX_FLAGS=$CFLAGS" -DCMAKE_INSTALL_PREFIX="$deps/ldc" \
    -DBUILD_SHARED_LIBS=OFF -DUSE_GNUTLS=OFF -DUSE_MBEDTLS=OFF -DUSE_NICE=OFF -DNO_WEBSOCKET=ON -DNO_EXAMPLES=ON \
    -DNO_TESTS=ON -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DOPENSSL_ROOT_DIR="$OPENSSL" -DOPENSSL_INCLUDE_DIR="$OPENSSL/include" \
    -DOPENSSL_SSL_LIBRARY="$OPENSSL/libssl.so" -DOPENSSL_CRYPTO_LIBRARY="$OPENSSL/libcrypto.so" > "$deps/ldc.log" 2>&1 \
    || { tail -30 "$deps/ldc.log" >&2; exit 1; }
ninja -C "$deps/ldc.build" -j "$JOBS" install >> "$deps/ldc.log" 2>&1 || { grep -E "error|FAILED" "$deps/ldc.log" | head -20 >&2; exit 1; }
mkdir -p "$deps/ldc/lib"
find "$deps/ldc.build" -name '*.a' -exec cp {} "$deps/ldc/lib/" \;

log libjpeg-turbo
# Scalar: its RISC-V vector code needs RVV 1.0, the C906 has 0.7.1.
"$CMAKE" -S "$src/libjpeg-turbo" -B "$deps/turbo.build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$toolchain" \
    -DCMAKE_BUILD_TYPE=Release "-DCMAKE_C_FLAGS=$CFLAGS" -DWITH_SIMD=OFF -DENABLE_SHARED=OFF -DWITH_TURBOJPEG=ON \
    -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DCMAKE_INSTALL_PREFIX="$deps/turbo" > "$deps/turbo.log" 2>&1 \
    || { tail -20 "$deps/turbo.log" >&2; exit 1; }
ninja -C "$deps/turbo.build" -j "$JOBS" install >> "$deps/turbo.log" 2>&1 || { tail -20 "$deps/turbo.log" >&2; exit 1; }
[ -d "$deps/turbo/lib" ] || ln -s lib64 "$deps/turbo/lib"

log "libcurl (to link against; the image's libcurl.so.4 runs)"
"$CMAKE" -S "$src/curl" -B "$deps/curl.build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$toolchain" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=ON -DBUILD_STATIC_LIBS=OFF -DBUILD_CURL_EXE=OFF -DBUILD_TESTING=OFF -DBUILD_LIBCURL_DOCS=OFF \
    -DBUILD_MISC_DOCS=OFF -DENABLE_CURL_MANUAL=OFF -DCURL_USE_OPENSSL=OFF -DCURL_ENABLE_SSL=OFF -DCURL_USE_LIBPSL=OFF \
    -DCURL_USE_LIBSSH2=OFF -DUSE_NGHTTP2=OFF -DCURL_BROTLI=OFF -DCURL_ZSTD=OFF -DCURL_ZLIB=OFF -DUSE_LIBIDN2=OFF \
    > "$deps/curl.log" 2>&1 || { tail -20 "$deps/curl.log" >&2; exit 1; }
ninja -C "$deps/curl.build" -j "$JOBS" >> "$deps/curl.log" 2>&1 || { tail -20 "$deps/curl.log" >&2; exit 1; }
curl_lib=$(find "$deps/curl.build" -name 'libcurl.so.4' | head -1)
[ -n "$curl_lib" ]

log "live555 (built with the server)"
patches "$src/live555" live555

log NanoKVM-Server
"$CMAKE" -S "$here" -B "$out/build" -G Ninja -DCMAKE_TOOLCHAIN_FILE="$toolchain" -DCMAKE_BUILD_TYPE=Release \
    "-DCMAKE_C_FLAGS=$CFLAGS" "-DCMAKE_CXX_FLAGS=$CFLAGS -ffile-prefix-map=$here=server-cpp" \
    -DLWS_ROOT="$deps/lws" -DOPENSSL_ROOT="$OPENSSL" -DGLAZE_ROOT="$src/glaze" -DRYML_HEADER="$src/rapidyaml.hpp" \
    -DKVM_INCLUDE="$here/../support/additional/kvm/include" -DKVM_LIB_DIR="$NATIVE" -DTURBOJPEG_ROOT="$deps/turbo" \
    -DSPNG_SRC="$src/libspng/spng" -DZLIB_ROOT="$ZLIB" -DCURL_INCLUDE="$src/curl/include" -DCURL_LIB="$curl_lib" \
    -DLDC_ROOT="$deps/ldc" -DLIVE555_SRC="$src/live555" > "$out/server.cmake.log" 2>&1 || { tail -30 "$out/server.cmake.log" >&2; exit 1; }
ninja -C "$out/build" -j "$JOBS" > "$out/server.log" 2>&1 || { grep -E "error|FAILED" "$out/server.log" | head -30 >&2; exit 1; }
for exe in "$out"/build/NanoKVM-Server "$out"/build/nkos-*; do
    [ -f "$exe" ] || continue
    "${CROSS}strip" --strip-unneeded --remove-section=.comment --remove-section=.note -o "$out/$(basename "$exe")" "$exe"
done
ls -l "$out"/NanoKVM-Server "$out"/nkos-*
