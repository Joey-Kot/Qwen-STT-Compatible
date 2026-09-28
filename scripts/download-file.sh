#!/usr/bin/env sh
# Fetch dependency archives and armored PGP files without caching partial transfers.
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  printf 'usage: %s URL OUTPUT [SHA256]\n' "$0" >&2
  exit 2
fi

url="$1"
out="$2"
expected_sha256="${3:-}"
part="$out.part"
max_attempts="${DOWNLOAD_MAX_ATTEMPTS:-6}"
retry_delay="${DOWNLOAD_RETRY_DELAY:-2}"

case "$max_attempts" in
  *[!0-9]*)
    printf '%s\n' 'DOWNLOAD_MAX_ATTEMPTS must be a positive integer' >&2
    exit 2
    ;;
esac
if [ "$max_attempts" -le 0 ]; then
  printf '%s\n' 'DOWNLOAD_MAX_ATTEMPTS must be a positive integer' >&2
  exit 2
fi

# A missing validator is a build-tool problem, not a corrupt download. Check
# before touching the cache so it cannot trigger deletion and repeated fetches.
case "$out" in
  *.tar.gz | *.tgz) validator=gzip ;;
  *.tar.xz | *.txz) validator=xz ;;
  *.tar.bz2 | *.tbz2) validator=bzip2 ;;
  *.asc) validator=awk ;;
  *)
    printf 'unsupported dependency file format: %s\n' "$out" >&2
    exit 2
    ;;
esac
if ! validator_path="$(command -v "$validator")"; then
  printf '%s is required to validate %s\n' "$validator" "$out" >&2
  exit 127
fi
if [ -n "$expected_sha256" ]; then
  if hash_tool="$(command -v sha256sum)"; then
    hash_style=sha256sum
  elif hash_tool="$(command -v shasum)"; then
    hash_style=shasum
  else
    printf '%s\n' 'sha256sum or shasum is required for SHA-256 verification' >&2
    exit 127
  fi
fi

# Check the entire compressed stream, including its trailer. Checking tar's file
# listing alone can miss a truncated compression trailer. GPG verification of
# the archive still runs in the upstream build script before extraction.
validate() {
  [ -s "$1" ] || return 1
  case "$validator" in
    awk)
      # Detect truncated legacy downloads; this is not signature verification.
      "$validator_path" '
        { sub(/\r$/, "") }
        /^-----BEGIN PGP (SIGNATURE|PUBLIC KEY BLOCK)-----$/ {
          end = $0; sub(/BEGIN/, "END", end); next
        }
        end != "" && $0 == end { complete = 1 }
        END { exit !complete }
      ' "$1" || return 1
      ;;
    *)
      "$validator_path" -t "$1" || return 1
      ;;
  esac
  if [ -n "$expected_sha256" ]; then
    if [ "$hash_style" = sha256sum ]; then
      digest="$("$hash_tool" "$1")" || return 1
    else
      digest="$("$hash_tool" -a 256 "$1")" || return 1
    fi
    digest="${digest%% *}"
    if [ "$digest" != "$expected_sha256" ]; then
      printf 'SHA-256 mismatch for %s: expected %s, got %s\n' "$out" "$expected_sha256" "$digest" >&2
      return 1
    fi
  fi
}

mkdir -p "$(dirname -- "$out")"
if [ -f "$out" ]; then
  if validate "$out"; then
    printf 'Using verified download cache: %s\n' "$out"
    exit 0
  fi
  printf 'Invalid cached download; recovering: %s\n' "$out" >&2
  if [ -f "$part" ]; then
    rm -f "$out"
  else
    mv "$out" "$part"
  fi
fi

attempt=1
while [ "$attempt" -le "$max_attempts" ]; do
  offset=0
  if [ -f "$part" ]; then
    offset="$(wc -c < "$part" | tr -d '[:space:]')"
  fi
  printf 'Downloading %s (attempt %s/%s, resume offset %s bytes)\n' "$url" "$attempt" "$max_attempts" "$offset"
  status=0
  # Use a new curl invocation per attempt so the resume offset is recalculated
  # after an interrupted transfer. --fail keeps HTTP error bodies out of .part.
  http_code="$(curl --disable --fail --show-error --silent --location \
    --connect-timeout "${DOWNLOAD_CONNECT_TIMEOUT:-20}" \
    --max-time "${DOWNLOAD_MAX_TIME:-1800}" \
    --speed-limit 1024 --speed-time 60 \
    --continue-at - --output "$part" --write-out '%{http_code}' "$url")" || status=$?

  case "$status:$http_code" in
    0:* | 33:* | *:416)
      # An already complete .part can cause HTTP 416 or a curl range error.
      if validate "$part"; then
        mv "$part" "$out"
        printf 'Download complete: %s\n' "$out"
        exit 0
      fi
      printf 'Incomplete/invalid file or unsupported resume; retrying from byte 0: %s\n' "$out" >&2
      rm -f "$part"
      ;;
    *)
      printf 'Download failed: curl=%s http=%s file=%s\n' "$status" "$http_code" "$out" >&2
      case "$http_code" in
        408 | 429) ;;
        4??) exit "$status" ;;
      esac
      # Local configuration, write errors, and certificate errors need a fix,
      # not repeated network requests. Other failures get bounded retries.
      case "$status" in
        2 | 3 | 4 | 23 | 26 | 27 | 48 | 60 | 77) exit "$status" ;;
      esac
      ;;
  esac
  if [ "$attempt" -lt "$max_attempts" ]; then
    sleep "$retry_delay"
  fi
  attempt=$((attempt + 1))
done

printf 'Download failed after %s attempts: %s; rerun to resume any retained %s\n' "$max_attempts" "$url" "$part" >&2
exit 1
