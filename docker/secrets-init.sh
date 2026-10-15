#!/bin/sh
# secrets-init — generates this install's JWT and token-encryption secrets for
# docker compose, so `docker compose up` needs no setup.
#
# Runs once per `docker compose up` as the secrets-init service (busybox), with
# the secrets volume mounted at /run/whatiff-secrets. On first run it writes
# three values (32 random bytes each, hex-encoded) to secrets.env. Every install
# gets its own values; nothing is shipped in the repository.
#
# An existing file is never regenerated: TOKEN_ENCRYPTION_SECRET has to keep
# decrypting what is already stored. The API loads the file through its -env
# flag, which never overrides a variable that is already set, so a value from
# the shell or .env still takes precedence over this file.
set -e
f=/run/whatiff-secrets/secrets.env
fail() { echo "secrets-init: $*" >&2; exit 1; }
# A usable secret is exactly 64 lowercase hex characters.
valid() { case "$1" in ''|*[!0-9a-f]*) return 1 ;; esac; [ "${#1}" -eq 64 ]; }
gen() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
if [ ! -s "$f" ]; then
  umask 077
  # Generated in this shell and checked before anything is written: a
  # failure inside $(gen) would otherwise produce an empty value.
  a=$(gen); b=$(gen); c=$(gen)
  for v in "$a" "$b" "$c"; do
    valid "$v" || fail "could not generate a secret (head, od or /dev/urandom failed)"
  done
  printf 'JWT_SECRET=%s\nJWT_REFRESH_SECRET=%s\nTOKEN_ENCRYPTION_SECRET=%s\n' "$a" "$b" "$c" > "$f.tmp"
  chown 65532:0 "$f.tmp" || fail "could not set the owner of $f.tmp; check the volume's filesystem permissions"
  chmod 400 "$f.tmp" || fail "could not set the mode of $f.tmp; check the volume's filesystem permissions"
  mv "$f.tmp" "$f"
fi
# Checked on every run: a file that is invalid now would otherwise stay
# invalid forever, since a non-empty file is never regenerated.
n=0
while IFS='=' read -r k v; do
  case "$k" in
    JWT_SECRET|JWT_REFRESH_SECRET|TOKEN_ENCRYPTION_SECRET) ;;
    *) fail "$f has an unexpected line ($k)" ;;
  esac
  valid "$v" || fail "$f has an invalid $k. Remove this project's secrets volume to generate new ones; anything already encrypted becomes unreadable."
  n=$((n + 1))
done < "$f"
[ "$n" -eq 3 ] || fail "$f should hold 3 secrets but holds $n"
# Re-asserted on every run, so ownership and mode cannot drift.
chown 65532:0 "$f" || fail "could not set the owner of $f; check the volume's filesystem permissions"
chmod 400 "$f" || fail "could not set the mode of $f; check the volume's filesystem permissions"
