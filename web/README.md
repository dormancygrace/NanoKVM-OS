# NanoKVM OS web interface

React/TypeScript browser interface derived from Sipeed NanoKVM. Product behavior is documented in [Video](../docs/VIDEO.md), [Updates](../docs/UPDATES.md) and the [main README](../README.md).

Use Node.js 22+ and pnpm 11+ as specified in `package.json`:

```sh
pnpm install --frozen-lockfile
pnpm lint
pnpm build
```

`dist/` is the build output. Use the matched application staging and signed-update workflow in [BUILD.md](../docs/BUILD.md). A frontend copied onto an unrelated server is not a validated release.

`pnpm dev` serves the interface on port 3001. Local API development requires a deliberately configured backend origin and authentication/CORS setup; the default development server is not an authenticated device proxy. For authentication, secure WebCodecs and release checks, serve the built interface from a matching HTTPS NanoKVM OS server. Do not disable authentication on a deployed KVM to work around development-origin errors.

If a browser retains stale resources after an update, force-refresh or clear this site's cached resources. Account/session data should not be included in bug reports.
