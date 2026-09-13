################################################################################
#
# nkos-addons
#
################################################################################

NKOS_ADDONS_VERSION = 1
NKOS_ADDONS_SITE = $(BR2_EXTERNAL_NANOKVM_PATH)/../addons/nkos-addons
NKOS_ADDONS_SITE_METHOD = local
NKOS_ADDONS_LICENSE = GPL-3.0-only
NKOS_ADDONS_LICENSE_FILES = LICENSE
NKOS_ADDONS_GOMOD = nanokvm.local/nkos-addons
# Keep the binary reproducible outside a Git checkout as well as in Buildroot;
# the external provenance binds it to the exact reviewed source bytes.
NKOS_ADDONS_GO_ENV = CGO_ENABLED=0 GOFLAGS=-buildvcs=false
NKOS_ADDONS_BIN_NAME = nkos-addons
NKOS_ADDONS_LDFLAGS = -s -w -buildid=

define NKOS_ADDONS_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0755 $(@D)/bin/nkos-addons $(TARGET_DIR)/usr/sbin/nkos-addons
endef

$(eval $(golang-package))
