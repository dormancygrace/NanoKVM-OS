# NanoKVM OS build entry points. See platform/README.md for pinned inputs.
.DEFAULT_GOAL := help
OUT ?= build/platform
JOBS ?= $(shell nproc)
# Pass -d for local test signing, or -e EC_KEY for release signing.
PLATFORM_ARGS ?=
PLATFORM = bash platform/build.sh -o "$(OUT)" -j "$(JOBS)" $(PLATFORM_ARGS)

.PHONY: help all platform app support vision web release-build package release clean

help:
	@echo "NanoKVM OS — pinned Alpine/platform build"
	@echo "  platform/all/release - Complete SD image and APKs (set PLATFORM_ARGS)"
	@echo "  release-build        - Build the components and write their hashes"
	@echo "  package              - Assemble and sign APKs from built components"
	@echo "  app/support/vision   - Rebuild server/board service/native libraries"
	@echo "  web                  - Build web/dist for frontend development"
	@echo "  clean                - Remove the platform APK builder and rootfs"
	@echo "  OUT=$(OUT) JOBS=$(JOBS)"
	@echo "Example: make platform PLATFORM_ARGS=-d"
	@echo "Official signing: PLATFORM_ARGS='-e path/to/key.key'"
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
	@$(PLATFORM) fetch toolchain kernel modules uboot opensbi fip initramfs boot native tpu system tools server web firmware hashes

package:
	@$(PLATFORM) payloads packages

web:
	@cd web && pnpm install --frozen-lockfile && pnpm build

clean:
	@$(PLATFORM) clean
