# Import SSH public keys

An administrator can open **Settings → System → Security → SSH public keys**,
choose a public key file or paste OpenSSH public keys, one per line, then select
**Import keys**, then confirm with the current web administrator password. The result shows the number added/skipped and SHA256 fingerprints
of newly added keys. Files are read locally and shown in the text area before
submission. The maximum input is 32 KiB / 100 non-comment key lines.

Keys grant SSH access as Linux **root**, not as the web-interface account.
Importing does not enable SSH or change its password/authentication policy.
Use the existing SSH control separately when SSH is disabled.

Supported public keys: Ed25519, RSA (at least 2048 bits), ECDSA P-256/P-384/P-521,
and OpenSSH FIDO security keys. Do not upload a private key. Certificates and
`authorized_keys` options are not accepted through this importer. Any malformed
line rejects the entire import. Blank lines and comments are allowed.

Existing `/root/.ssh/authorized_keys` contents, comments and restrictions are
preserved. Duplicate public-key material is skipped even when comments differ
or the existing key has restrictions; importing cannot remove those restrictions.
New entries are appended with an atomic replacement, directory mode 0700 and file
mode 0600. Imports are serialized and symlink/non-regular targets are refused.
The GUI does not remove keys or replace the existing list.

[Settings backup and reset](settings-recovery.md) includes this same authorized-keys
file in its users category: **Keep users** retains it, independently of **Keep apps**.

Imports are logged with the administrator, connection address and added key
fingerprints. Passwords and full public-key text are not logged.
