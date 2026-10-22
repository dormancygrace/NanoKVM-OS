#!/bin/sh
# Require an unencrypted ECDSA signing key matching the supplied public key.
set -eu
[ "$#" = 2 ] || { echo "usage: check-ec-key.sh PRIVATE PUBLIC" >&2; exit 2; }
if grep -qE '^-----BEGIN ENCRYPTED PRIVATE KEY-----|^Proc-Type: 4,ENCRYPTED' "$1"; then
    echo "Signing key must not be encrypted" >&2; exit 1
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
openssl pkey -in "$1" -passin pass: -pubout -out "$work/public.pem" 2>/dev/null || {
    echo "Signing key must be an unencrypted PEM private key" >&2; exit 1;
}
openssl pkey -pubin -in "$work/public.pem" -text -noout |
    grep -qxE 'ASN1 OID: (prime256v1|secp384r1)' || {
    echo "Signing key must be ECDSA P-256 or P-384" >&2; exit 1;
}
openssl pkey -pubin -in "$work/public.pem" -outform DER > "$work/private-public.der"
openssl pkey -pubin -in "$2" -outform DER > "$work/expected.der"
cmp -s "$work/private-public.der" "$work/expected.der" || {
    echo "Signing key does not match its published public key" >&2; exit 1;
}
