################################################################################
# superfile
################################################################################
NKOS_SUPERFILE_VERSION = 1.6.0
NKOS_SUPERFILE_SITE = $(call github,yorukot,superfile,v$(NKOS_SUPERFILE_VERSION))
NKOS_SUPERFILE_LICENSE = MIT
NKOS_SUPERFILE_LICENSE_FILES = LICENSE NOTICE.md
NKOS_SUPERFILE_GOMOD = github.com/yorukot/superfile
NKOS_SUPERFILE_GO_ENV = CGO_ENABLED=0
NKOS_SUPERFILE_BIN_NAME = spf
NKOS_SUPERFILE_LDFLAGS = -s -w

define NKOS_SUPERFILE_ALIAS
	ln -sf spf $(TARGET_DIR)/usr/bin/superfile
	$(INSTALL) -D -m 0644 $(@D)/LICENSE $(TARGET_DIR)/usr/share/licenses/superfile/LICENSE
	$(INSTALL) -m 0644 $(@D)/NOTICE.md $(TARGET_DIR)/usr/share/licenses/superfile/NOTICE.md
endef
NKOS_SUPERFILE_POST_INSTALL_TARGET_HOOKS += NKOS_SUPERFILE_ALIAS
$(eval $(golang-package))
