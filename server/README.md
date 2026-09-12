# NanoKVM Server

This is the NanoKVM OS backend, derived from Sipeed NanoKVM.

See the [project documentation](../README.md) for this firmware; the [Sipeed wiki](https://wiki.sipeed.com/nanokvm) documents upstream hardware and firmware.

## Structure

```shell
server
├── common       // Common utility components
├── config       // Server configuration
├── dl_lib       // Shared object libraries
├── include      // Header files for shared objects
├── logger       // Logging system
├── middleware   // Server middleware components
├── proto        // API request/response definitions
├── router       // API route handlers
├── service      // Core service implementations
├── utils        // Utility functions
└── main.go
```

## Configuration

The configuration file path is `/etc/kvm/server.yaml`.

```yaml
# Network Settings
proto: https           # Default; a unique self-signed certificate is created when absent
host: ""               # The listening address for the HTTP/HTTPS service. If left empty, all network interfaces will be bound
port:
    http: 80           # The listening port for the HTTP service. Default is `80`
    https: 443         # The listening port for the HTTPS service (effective when HTTPS is enabled). Default is `443`
cert:
    crt: /etc/kvm/server.crt    # The path to the public key certificate for HTTPS
    key: /etc/kvm/server.key    # The path to the private key file for HTTPS


# Logging Configuration
logger:
    level: info     # Global log output level. Evaluated options from highest to lowest detail: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`. Default is `info`
    file: stdout    # Log output destination. `stdout` outputs to the standard console. A file path directs log output to that file. Default is `stdout`


# Authentication & Security
authentication: enable              # Whether to enable identity verification for HTTP API and Web endpoints. Options are `enable` or `disable`. Default is `enable`. Highly recommended to leave this enabled for internet-facing devices!
jwt:
   secretKey: ""                    # The secret key used to sign and verify JWT Tokens. If left empty, a random key will be generated automatically on startup
   refreshTokenDuration: 2678400    # The token refresh duration threshold in seconds before forcing a re-login. Default is `2678400` (~31 days)
   revokeTokensOnLogout: true       # Whether logout invalidates all sessions belonging to that user. Other users are never logged out. Setting this to false only clears the browser cookie and is not recommended. Default is `true`
security:
   loginLockoutDuration: 0          # The duration (in seconds) to ban an IP from attempting to log in again after reaching the failure limit. If set to `0` or left empty, brute-force protection is disabled. Default is `0`
   loginMaxFailures:     5          # The maximum number of continuous failed login attempts allowed per IP before triggering protection. Default is `5`
   trustedProxies:                  # IP addresses or CIDRs allowed to supply X-Forwarded-Host and X-Forwarded-Proto. Default: loopback only.
     - 127.0.0.1/32
     - ::1/128
   allowedOrigins: []               # Optional exact additional browser origins, including scheme and host (and port when non-default).


# WebRTC Traversal Settings
stun: stun.l.google.com:19302 # The default STUN server address used for NAT hole-punching to establish P2P streams
turn:
    turnAddr: example_addr    # The relay (TURN) server address (format `ip:port`) used as a fallback when P2P connection fails. Leave empty to disable TURN relay
    turnUser: example_user    # The username required for authorization to the TURN server
    turnCred: example_cred    # The credential/password required for authorization to the TURN server
```

## Web Users

NanoKVM uses two device-wide roles:

- `admin`: KVM access plus user, system, network, update, storage, terminal, script, MCP, and PicoClaw administration.
- `user`: KVM video, keyboard, mouse, paste, power/reset, and Wake-on-LAN access.

Administrators manage accounts from **Settings > Account**. Account data remains in
`/etc/kvm/pwd`; the server migrates the legacy single-account JSON format in place and writes
the multi-user format atomically with mode `0600`. Keeping the same path preserves the physical
BOOT-button password reset behavior.
Users must confirm their current password when changing it themselves; administrators can reset
non-owner users from the account manager. The device owner can rename their own account and change
their own password; this password is also synchronized to the Linux root account. The owner-account
restrictions prevent accidental changes through account management; an administrator with terminal
or script access remains a device administrator, not a sandboxed user.

All authenticated sessions are backed by the current account state. Disabling, deleting,
changing the role or password of a user invalidates that user's HTTP and real-time connections
without affecting other users. Multiple users may watch and control the KVM concurrently; input
uses the existing cooperative HID coordinator, so simultaneous input can interleave.
Video mode, quality, resolution, and MJPEG frame-detection controls remain shared KVM
operations; when several users adjust them concurrently, the latest change applies device-wide.

## Reverse Proxy

When NanoKVM is behind a reverse proxy, preserve the browser-visible host and scheme. WebSocket
endpoints (HID, H.264, terminal, and PicoClaw) validate `Origin`, so omitting these headers can
make the WebSocket handshake fail with `403` even when ordinary HTTP pages work.

The following nginx example uses the default HTTPS backend on the same machine. Configure backend certificate trust as appropriate for your deployment:

```nginx
location / {
    proxy_pass https://127.0.0.1:443;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

The last proxy hop must overwrite (not append) `Host`, `X-Forwarded-Host`, and
`X-Forwarded-Proto` with the browser-visible values, and clear any client-supplied forwarded
headers. In a multi-hop setup, configure every trusted hop to pass the values to the next hop and
ensure the final hop is the one listed in `security.trustedProxies`. Older configurations using
`Host $proxy_host` must be migrated; that value names the upstream and can make WebSocket Origin
checks fail (especially when the public URL uses a non-default port).

NanoKVM accepts `X-Forwarded-Host` and `X-Forwarded-Proto` only when the direct peer address is
in `security.trustedProxies`. The default trusts only `127.0.0.1/32` and `::1/128`; add the IP or
CIDR of a non-loopback proxy, never an untrusted client network. For a proxy that cannot preserve
the original host, `security.allowedOrigins` may list explicit full origins such as
`https://kvm.example.com` (exact scheme, host, and port match only). Prefer the forwarded headers;
`allowedOrigins` is an exception list, not a wildcard or a replacement for proxy trust.

## API and Automation Authentication

`POST /api/auth/login` is a browser login by default: it sets an HttpOnly session cookie and returns
the normal success response without a token. Automation clients can opt in to the JWT response by
sending `X-NanoKVM-Return-Token: true`; the cookie is still set. Send that JWT on later requests as
`Authorization: Bearer <JWT>`. If an `Authorization` header is present but invalid, NanoKVM returns
unauthorized rather than falling back to a browser cookie.
Clients that previously read `data.token` from login responses must add this header before upgrading;
the default browser-login response intentionally does not retain that legacy field.

Passwords in the authentication and user-management APIs are not plaintext. They use the CryptoJS /
OpenSSL salted passphrase format with passphrase `nanokvm-sipeed-2024`. This is transport
obfuscation; always use HTTPS for remote access. The shell function below returns raw Base64 (the
`-md md5` option is required for CryptoJS compatibility). Use it with `curl --data-urlencode`,
which performs the HTTP form encoding exactly once; JSON clients may send the legacy
percent-encoded CryptoJS value.

```sh
encrypt_password() {
  printf %s "$1" | openssl enc -aes-256-cbc -salt -a -A -md md5 \
    -pass pass:nanokvm-sipeed-2024
}
```

For example, log in and request a token (requires `jq` to extract it):

```sh
BASE='https://kvm.example.com'
TOKEN=$(curl --fail --silent --show-error \
  -H 'X-NanoKVM-Return-Token: true' \
  --data-urlencode 'username=admin' \
  --data-urlencode "password=$(encrypt_password 'replace-with-password')" \
  "$BASE/api/auth/login" | jq -er '.data.token')
curl --fail --silent --show-error \
  -H "Authorization: Bearer $TOKEN" "$BASE/api/auth/account"
```

An administrator can create users, rename non-owner users, and reset non-owner passwords. The
device owner may rename only their own account; the following owner-token example renames the
default `admin` owner account:

```sh
curl --fail --silent --show-error -X POST -H "Authorization: Bearer $TOKEN" \
  --data-urlencode 'username=operator' \
  --data-urlencode "password=$(encrypt_password 'operator-password')" \
  --data-urlencode 'role=user' "$BASE/api/auth/users"
curl --fail --silent --show-error -X POST -H "Authorization: Bearer $TOKEN" \
  --data-urlencode "password=$(encrypt_password 'new-operator-password')" \
  "$BASE/api/auth/users/operator/password"
curl --fail --silent --show-error -X PUT -H "Authorization: Bearer $TOKEN" \
  --data-urlencode 'username=owner' "$BASE/api/auth/users/admin"
```

Renaming the signed-in owner revokes `$TOKEN`, so the rename is deliberately the last command;
sign in again with the new username before making more API calls.

To change the signed-in user's own password, post both encrypted fields to
`/api/auth/password`: `currentPassword` and `password`. Earlier clients that posted
`{username, password}` to this endpoint must migrate; the old request body is not accepted.

## Build and update

Use the matched toolchain, custom Go runtime and native libraries in [BUILD.md](../docs/BUILD.md). The legacy upstream toolchain command is not a release-equivalent NanoKVM OS build.

Install compatible signed `.nkos` packages through **Settings → Updates**; see [UPDATES.md](../docs/UPDATES.md). Do not install original NanoKVM application archives over NanoKVM OS. Kernel changes require a compatible format-3 package and the kernel-capable updater, or a matching full image. Native-library compatibility remains mandatory.
