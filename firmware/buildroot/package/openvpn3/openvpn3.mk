# Pinned OpenVPN 3 Core with the upstream Linux 6.16+ ovpn netlink ABI.
# Release 3.11.7 still targets ovpn-dco-v2 and cannot offload to our kernel.
OPENVPN3_VERSION = 87fd40bb583cc89787c0bf4da79744f0e6d02ac3
OPENVPN3_SITE = $(call github,OpenVPN,openvpn3,$(OPENVPN3_VERSION))
OPENVPN3_LICENSE = MPL-2.0, GPL-3.0+ (NanoKVM adapter)
OPENVPN3_LICENSE_FILES = LICENSE.md
OPENVPN3_DEPENDENCIES = nkos-asio openssl lz4 fmt libnl host-pkgconf
OPENVPN3_SUBDIR = nkos
OPENVPN3_CONF_OPTS = -DCORE_SOURCE=$(@D)
define OPENVPN3_PREPARE_ADAPTER
	mkdir -p $(@D)/nkos
	cp -a $(BR2_EXTERNAL_NANOKVM_PATH)/../vpn/. $(@D)/nkos/
	$(HOST_DIR)/bin/python3 $(@D)/nkos/integrate.py $(@D)
endef
OPENVPN3_PRE_CONFIGURE_HOOKS += OPENVPN3_PREPARE_ADAPTER
$(eval $(cmake-package))
