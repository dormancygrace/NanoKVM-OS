# NanoKVM OS build entry points. See platform/README.md for pinned inputs.
.DEFAULT_GOAL := help
OUT ?= build/platform
JOBS ?= $(shell nproc)
# Pass -d for local test signing, or -k RSA_KEY -e EC_KEY for release signing.
PLATFORM_ARGS ?=
PLATFORM = bash platform/build.sh -o "$(OUT)" -j "$(JOBS)" $(PLATFORM_ARGS)

.PHONY: help all platform app support vision web release-build package release clean \
        test-wifi-modules test-alpine-network test-wifi-runtime \
        test-ethernet-runtime test-usb-profiles test-runtime-state

help:
	@echo "NanoKVM OS — pinned Alpine/platform build"
	@echo "  platform/all/release - Complete SD image and APKs (set PLATFORM_ARGS)"
	@echo "  release-build        - Build the components and write their hashes"
	@echo "  package              - Assemble and sign APKs from built components"
	@echo "  app/support/vision   - Rebuild server/board service/native libraries"
	@echo "  web                  - Build web/dist for frontend development"
	@echo "  clean                - Remove the platform APK builder and rootfs"
	@echo "  test-*               - Host-side network, USB and filesystem checks"
	@echo "  OUT=$(OUT) JOBS=$(JOBS)"
	@echo "Example: make platform PLATFORM_ARGS=-d"
	@echo "Official signing: PLATFORM_ARGS='-k path/to/key.rsa -e path/to/key.key'"
	@echo "Versions come from firmware/alpine/release.env; see platform/README.md."

all release: platform

platform:
	@$(PLATFORM)

# Component targets consume the prepared platform source and toolchain trees.
app:
	@$(PLATFORM) server

support:
	@$(PLATFORM) system

vision:
	@$(PLATFORM) native

release-build:
	@$(PLATFORM) fetch toolchain kernel modules uboot opensbi fip initramfs boot native system server web tools firmware hashes

package:
	@$(PLATFORM) payloads packages

web:
	@cd web && pnpm install --frozen-lockfile && pnpm build

clean:
	@$(PLATFORM) clean

test-wifi-modules:
	@python3 tests/test-wifi-selector.py
	@python3 tests/test-aic-firmware-path.py

test-alpine-network:
	@python3 tests/test-alpine-network.py
	@python3 tests/test-alpine-service-deps.py

test-wifi-runtime:
	@sh tests/test-s30wifi-runtime.sh
	@cd server && go test -tags teststub,nomsgpack ./service/network

test-ethernet-runtime:
	@sh tests/test-ethernet-enabled.sh
	@cd server && go test -tags teststub,nomsgpack ./service/network

test-usb-profiles:
	@sh tests/test-usb-profiles.sh
	@sh tests/test-usb-rebind.sh

test-runtime-state:
	@sh tests/test-runtime-state.sh
