.PHONY: web updater-test
web:
	cd web && pnpm install --frozen-lockfile && pnpm build
updater-test:
	cd server && CGO_ENABLED=0 go test ./osupdate ./cmd/nkos-update ./cmd/nkos-package
