# Standalone Asio headers used by the OpenVPN 3 client.
NKOS_ASIO_VERSION = asio-1-36-0
NKOS_ASIO_SITE = $(call github,chriskohlhoff,asio,$(NKOS_ASIO_VERSION))
NKOS_ASIO_LICENSE = BSL-1.0
NKOS_ASIO_LICENSE_FILES = asio/LICENSE_1_0.txt
NKOS_ASIO_INSTALL_STAGING = YES
NKOS_ASIO_INSTALL_TARGET = NO
define NKOS_ASIO_INSTALL_STAGING_CMDS
	cp -a $(@D)/asio/include/. $(STAGING_DIR)/usr/include/
endef
$(eval $(generic-package))
