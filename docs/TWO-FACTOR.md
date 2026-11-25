# Two-factor authentication

Each web account can add a second factor to its password: a time-based one-time password (TOTP, RFC 6238) from an authenticator app such as Google Authenticator, Microsoft Authenticator or 2FAS, or one of ten recovery codes.

## Turning it on

**Settings → Account → Two-factor authentication → Set up** shows a QR code and the key it holds. After the app shows its first code, entering it turns the second factor on and shows ten recovery codes once, to download or copy. Each recovery code signs in once instead of an app code.

Codes are six digits, change every 30 seconds and use HMAC-SHA1, the defaults of every authenticator app. A code is accepted one step (30 s) early or late, and never twice: the step of the last accepted code is kept with the account.

## Signing in

After a correct password the sign-in page asks for the code. The password check returns code `-6` with a `challenge`; `POST /api/auth/login/totp` with `{challenge, otp}` finishes the sign-in within five minutes, from the same address, with at most five codes. Wrong codes count toward the same lockout as wrong passwords, also across new challenges. A browser whose address changes between the two steps gets code `-8` and signs in again.

Scripts that sign in with one request send the code with the password: `POST /api/auth/login` with `otp` next to `username` and `password` (and the `X-NanoKVM-Return-Token: true` header for a bearer token). An account used by scripts that cannot produce codes is better left without a second factor.

Codes need the device clock. A NanoKVM that booted without network or a set clock rejects app codes with code `-7` and says so; recovery codes still work.

## Turning it off and losing the phone

- **The user**: **Turn off**, or **New recovery codes** (the old ones stop working), with the password and an app or recovery code.
- **An administrator**: **Settings → Account → Users → Reset 2FA** turns it off for another account. As with passwords, not for the device owner account.
- **The console**: as root over SSH or the serial console, `nkos-totp-reset USER` turns it off for any account. The running server reads the change with the account file.

**Settings → System → Backup & reset** treats the second factor as part of the account: a reset with **Keep users** keeps it, a reset without it returns to `admin` / `admin` without a second factor, and restoring a backup brings back the second factor each account had when the backup was made, with that backup's recovery codes.

## Confirming the password for actions that act as root

A session lasts for weeks, so a left-open or stolen one must not be enough for
what acts as root on the device. These ask the signed-in user to confirm the
password, and the second factor when the account has one; the confirmation
lasts five minutes for that session:

- the terminal;
- uploading, running and starting scripts at startup, saving and running
  scheduled tasks;
- creating, changing and deleting accounts, setting their passwords and
  resetting their second factor;
- turning SSH on (which sets root's password);
- installing or removing a custom TLS certificate.

The page asks for the password when one of these is refused, then repeats the
action. API clients call `POST /api/auth/recheck` with `password`,
`passwordEncoding` and `otp`; until then those routes answer HTTP 403 with code
`-11`. `GET /api/auth/recheck` reports `fresh` (a confirmation is recent) and
`totp` (a confirmation needs the second factor).

## Storage

The secret, the last accepted step and SHA-256 hashes of the unused recovery codes are stored with the account in `/etc/kvm/pwd` (mode 0600), which settings backups carry and the diagnostic archive leaves out. Accounts without a second factor are stored as before. A release without two-factor authentication drops the field when it rewrites the file, so after a downgrade the second factor is off.

## API

| Request | Who | Body | Result |
| --- | --- | --- | --- |
| `POST /api/auth/login` | anyone | `username`, `password`, optional `otp` | `-6` with `challenge` when a code is needed |
| `POST /api/auth/login/totp` | anyone | `challenge`, `otp` | the session, as a login |
| `GET /api/auth/account` | signed in | | `totpEnabled`, `recoveryCodesLeft` |
| `POST /api/auth/totp/setup` | signed in | | `secret`, `uri` (otpauth) for ten minutes |
| `POST /api/auth/totp/enable` | signed in | `otp` | `recoveryCodes` |
| `POST /api/auth/totp/recovery-codes` | signed in | `password`, `otp` | new `recoveryCodes` |
| `POST /api/auth/totp/disable` | signed in | `password`, `otp` | |
| `DELETE /api/auth/users/:username/totp` | administrators | | off for another account, not the device owner |
| `GET /api/auth/users` | administrators | | `totpEnabled` per user |

For all account requests and SSH setup, send passwords over HTTPS with `passwordEncoding: "plain"`.
The server uses the supplied password verbatim, including percent escapes. For compatibility with the original API,
omitting the field or setting it to `"cryptojs"` selects the salted CryptoJS format. Send its Base64 value directly
or percent-encoded. A malformed legacy value is rejected before any password change.
