################################################################################
#
# nkos-apk-tools
#
################################################################################

NKOS_APK_TOOLS_VERSION = 3.0.8
NKOS_APK_TOOLS_SOURCE = apk-tools-v$(NKOS_APK_TOOLS_VERSION).tar.bz2
NKOS_APK_TOOLS_SITE = https://gitlab.alpinelinux.org/alpine/apk-tools/-/archive/v$(NKOS_APK_TOOLS_VERSION)
NKOS_APK_TOOLS_LICENSE = GPL-2.0-only
NKOS_APK_TOOLS_LICENSE_FILES = LICENSE
NKOS_APK_TOOLS_CPE_ID_VENDOR = apk-tools
NKOS_APK_TOOLS_CPE_ID_PRODUCT = apk-tools
NKOS_APK_TOOLS_DEPENDENCIES = host-nkos-apk-tools host-pkgconf openssl zlib zstd
NKOS_APK_TOOLS_CONF_OPTS = \
	-Darch=riscv64 \
	-Dcrypto_backend=openssl \
	-Ddocs=disabled \
	-Dhelp=disabled \
	-Dlua=disabled \
	-Dminimal=false \
	-Dpython=disabled \
	-Dtests=disabled \
	-Durl_backend=libfetch \
	-Dzstd=enabled

HOST_NKOS_APK_TOOLS_DEPENDENCIES = host-pkgconf host-openssl host-zlib host-zstd
HOST_NKOS_APK_TOOLS_CONF_OPTS = \
	-Darch=x86_64 \
	-Dcrypto_backend=openssl \
	-Ddocs=disabled \
	-Dhelp=disabled \
	-Dlua=disabled \
	-Dminimal=false \
	-Dpython=disabled \
	-Dtests=disabled \
	-Durl_backend=libfetch \
	-Dzstd=enabled

$(eval $(meson-package))
$(eval $(host-meson-package))
