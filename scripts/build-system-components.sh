#!/bin/sh
# Cross-build isolated network tools; never replace the vendor media libraries.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
base=${COMPONENT_BUILD_DIR:-$root/build/components}
prefix=/kvmapp/system/modern
stage=$base/stage$prefix
export CC=${CC:-riscv64-unknown-linux-musl-gcc}
cross=${CC%gcc}
export AR=${AR:-${cross}ar} RANLIB=${RANLIB:-${cross}ranlib}
sh "$root/scripts/build-network-libc.sh" "$base" "$CC"
realcc=$(command -v "$CC")
sysroot=$("$realcc" -print-sysroot)
mkdir -p "$base/toolchain-libs"
cp "$("$realcc" -print-file-name=libatomic.a)" "$base/toolchain-libs/"
cat > "$base/network-cc" <<EOF
#!/bin/sh
exec "$realcc" -specs="$base/musl/lib/musl-gcc.specs" -idirafter "$sysroot/usr/include" -L"$base/toolchain-libs" "\$@"
EOF
chmod 755 "$base/network-cc"
export CC="$base/network-cc"
export CFLAGS='-Os -march=rv64gc -mabi=lp64d'
jobs=${JOBS:-8}
mkdir -p "$base/sources" "$base/build" "$stage"
fetch() {
 archive=$1; digest=$2; url=$3
 test -f "$base/sources/$archive" || curl -fL --retry 2 -o "$base/sources/$archive" "$url"
 printf '%s  %s\n' "$digest" "$base/sources/$archive" | sha256sum -c -
 tar -xf "$base/sources/$archive" -C "$base/build"
}
fetch openssl-3.5.8.tar.gz a8f84a39918ec6415ce765d9b429d313ba97b8143169c172e734b9514464f5b2 https://github.com/openssl/openssl/releases/download/openssl-3.5.8/openssl-3.5.8.tar.gz
fetch openssh-10.5p1.tar.gz d44d28a839ea9daf969cc69150fde59910b2b39361dad81a3bd6cbd19218db11 https://cdn.openbsd.org/pub/OpenBSD/OpenSSH/portable/openssh-10.5p1.tar.gz
fetch curl-8.22.0.tar.xz f7ef3ae8a22e521f289803fe93543eb64c329b58aa73a9e224dfd915a2a5f4f7 https://curl.se/download/curl-8.22.0.tar.xz
fetch zlib-1.3.2.tar.gz bb329a0a2cd0274d05519d61c667c062e06990d72e125ee2dfa8de64f0119d16 https://zlib.net/zlib-1.3.2.tar.gz
fetch libnl-3.12.0.tar.gz fc51ca7196f1a3f5fdf6ffd3864b50f4f9c02333be28be4eeca057e103c0dd18 https://github.com/thom311/libnl/releases/download/libnl3_12_0/libnl-3.12.0.tar.gz
fetch wpa_supplicant-2.12.tar.gz 08e23937e16d0155e55cab2b51f51fbe10d80a1aa91c4e15442645059b737ef6 https://w1.fi/releases/wpa_supplicant-2.12.tar.gz
(
 cd "$base/build/openssl-3.5.8"
 perl Configure linux64-riscv64 --prefix="$prefix" --openssldir=/etc/ssl --libdir=lib no-shared no-module no-tests no-asm -static $CFLAGS
 make -j"$jobs"
 make DESTDIR="$base/stage" install_sw
)
(
 cd "$base/build/zlib-1.3.2"
 ./configure --static --prefix="$prefix"
 make -j"$jobs"
 make DESTDIR="$base/stage" install
)
export CPPFLAGS="-I$stage/include" LDFLAGS="-static -L$stage/lib"
export PKG_CONFIG_LIBDIR="$stage/lib/pkgconfig" PKG_CONFIG_SYSROOT_DIR="$base/stage"
(
 cd "$base/build/openssh-10.5p1"
 ./configure --host=riscv64-unknown-linux-musl --prefix="$prefix" --sysconfdir=/etc/ssh --libexecdir=/usr/libexec --with-ssl-dir="$stage" --with-zlib="$stage" --with-privsep-path=/var/empty --disable-strip
 make -j"$jobs"
 make DESTDIR="$base/stage" install-nokeys
)
(
 cd "$base/build/curl-8.22.0"
 ./configure --host=riscv64-unknown-linux-musl --prefix="$prefix" --disable-shared --enable-static --with-openssl="$stage" --with-zlib="$stage" --without-libpsl --without-libidn2 --without-brotli --without-zstd --without-nghttp2 --without-nghttp3 --without-libssh2 --with-ca-bundle=/etc/ssl/certs/ca-certificates.crt --with-ca-path=/etc/ssl/certs
 make -j"$jobs" CURL_LDFLAGS_BIN=-all-static
 make DESTDIR="$base/stage" install
)
(
 cd "$base/build/libnl-3.12.0"
 ./configure --host=riscv64-unknown-linux-musl --prefix="$prefix" --disable-shared --enable-static --disable-cli
 make -j"$jobs"
 make DESTDIR="$base/stage" install
)
(
 cd "$base/build/wpa_supplicant-2.12/wpa_supplicant"
 cp defconfig .config
 sed -i '/^CONFIG_CTRL_IFACE_DBUS/s/^/#/' .config
 cat >> .config <<EOF
CONFIG_DRIVER_WEXT=y
CONFIG_DRIVER_NL80211=y
CONFIG_LIBNL32=y
CONFIG_TLS=openssl
CONFIG_SAE=y
CONFIG_OWE=y
CONFIG_IEEE80211W=y
CONFIG_CTRL_IFACE=y
CONFIG_MATCH_IFACE=y
CONFIG_INTERNAL_LIBTOMMATH=y
CONFIG_DEBUG_SYSLOG=y
CFLAGS += $CFLAGS -I$stage/include -I$stage/include/libnl3
LIBS += -L$stage/lib -lnl-genl-3 -lnl-3 -lm -lpthread
LIBS_p += -L$stage/lib
LIBS_c += -L$stage/lib
EOF
 make -j"$jobs" CC="$CC"
 install -m755 wpa_supplicant wpa_cli wpa_passphrase "$stage/sbin/"
)
# Keep runtime payload, licenses and exact sources (including LGPL libnl).
out=$root/kvmapp/system/modern
mkdir -p "$out/bin" "$out/sbin" "$out/libexec" "$out/share/sources"
for f in openssl curl ssh scp sftp ssh-add ssh-agent ssh-keygen ssh-keyscan; do install -m755 "$stage/bin/$f" "$out/bin/$f"; done
for f in sshd wpa_supplicant wpa_cli wpa_passphrase; do install -m755 "$stage/sbin/$f" "$out/sbin/$f"; done
for f in sftp-server ssh-keysign ssh-pkcs11-helper ssh-sk-helper sshd-auth sshd-session; do install -m755 "$base/stage/usr/libexec/$f" "$out/libexec/$f"; done
"${cross}strip" "$out/bin/"* "$out/sbin/"* "$out/libexec/"*
for f in "$out/bin/"* "$out/sbin/"* "$out/libexec/"*; do
 if "${cross}readelf" -l "$f" | grep -q INTERP; then
  echo "Unexpected dynamic libc dependency: $f" >&2
  exit 1
 fi
done
cp "$base/sources/"*.tar.* "$out/share/sources/"
cp "$root/scripts/build-system-components.sh" "$root/scripts/build-network-libc.sh" "$out/share/sources/"
cp "$base/sources/"*.patch "$out/share/sources/"
cp "$base/build/musl-1.2.6/COPYRIGHT" "$out/share/musl-COPYRIGHT"
cp "$base/build/openssl-3.5.8/LICENSE.txt" "$out/share/openssl-LICENSE.txt"
cp "$base/build/openssh-10.5p1/LICENCE" "$out/share/openssh-LICENCE"
cp "$base/build/curl-8.22.0/COPYING" "$out/share/curl-COPYING"
cp "$base/build/zlib-1.3.2/README" "$out/share/zlib-README"
cp "$base/build/libnl-3.12.0/COPYING" "$out/share/libnl-COPYING"
cp "$base/build/wpa_supplicant-2.12/README" "$out/share/wpa-README"
# Relinkable libnl objects are retained for the LGPL static-linking requirement.
cp "$base/build/wpa_supplicant-2.12/wpa_supplicant/.config" "$out/share/sources/wpa.config"
tar -cf "$out/share/sources/wpa-build-objects.tar" -C "$base/build/wpa_supplicant-2.12" build
(cd "$out" && find bin sbin libexec share -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)