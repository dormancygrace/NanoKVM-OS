#!/bin/sh
# Build an isolated libc; vendor toolchain waitpid returns raw negative errno.
set -eu
base=$1
cc=$2
cross=${cc%gcc}
mkdir -p "$base/sources" "$base/build" "$base/musl"
get() {
 name=$1; hash=$2; url=$3
 test -f "$base/sources/$name" || curl -fL --retry 2 --connect-timeout 10 -o "$base/sources/$name" "$url"
 printf '%s  %s\n' "$hash" "$base/sources/$name" | sha256sum -c -
}
# Buildroot's mirror carries the official archive, checked against its
# upstream-signature-verified hash in package/musl/musl.hash.
get musl-1.2.6.tar.gz d585fd3b613c66151fc3249e8ed44f77020cb5e6c1e635a616d3f9f82460512a https://sources.buildroot.net/musl/musl-1.2.6.tar.gz
get musl-iconv.patch 444fa70e52ca158fb7d4bad560637790bbf8f72e80b82fff840dd66fa83091e3 https://www.openwall.com/lists/musl/2026/04/03/2/1
get musl-tls32.patch 1ee29f64f9ca8e8ad7c349779d661ff6b52126a27575d3586981357a52c406fb https://www.openwall.com/lists/musl/2026/04/10/3/1
if [ ! -f "$base/musl/lib/libc.a" ]; then
 tar -xf "$base/sources/musl-1.2.6.tar.gz" -C "$base/build"
 (
  cd "$base/build/musl-1.2.6"
  patch -p1 < "$base/sources/musl-iconv.patch"
  patch -p1 < "$base/sources/musl-tls32.patch"
  CC="$cc" AR="${cross}ar" RANLIB="${cross}ranlib" ./configure --target=riscv64-unknown-linux-musl --prefix="$base/musl" --disable-shared CFLAGS='-Os -march=rv64gc -mabi=lp64d'
  make -j"${JOBS:-8}"
  make install
 )
fi
sh "$base/build/musl-1.2.6/tools/musl-gcc.specs.sh" "$base/musl/include" "$base/musl/lib" /lib/ld-musl-riscv64.so.1 > "$base/musl/lib/musl-gcc.specs"