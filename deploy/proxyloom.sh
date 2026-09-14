#!/usr/bin/env bash
# Linux Docker Engine operator entry point. No service is exposed beyond loopback.
set -euo pipefail
umask 077
[[ $EUID == 0 ]] || { echo 'Run with sudo on the Linux Docker host.' >&2; exit 1; }
release=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
state=${PROXYLOOM_STATE_DIR:?Set PROXYLOOM_STATE_DIR to a dedicated absolute directory}
[[ $state =~ ^/[A-Za-z0-9_./-]+$ && $state != / && $state != */../* && $state != */.. ]] || { echo 'Invalid state directory.' >&2; exit 1; }
export PROXYLOOM_STATE_DIR=$state
arch=$(uname -m)
case $arch in x86_64) arch=amd64;; aarch64) arch=arm64;; *) echo 'Unsupported architecture.' >&2; exit 1;; esac
[[ -r $release/images-$arch.env ]] || { echo 'Load the matching release image package first.' >&2; exit 1; }
# This file is part of the release covered by SHA256SUMS, not user data.
set -a
source "$release/images-$arch.env"
set +a
for service in API RUNNER OPERATIONS POSTGRES; do
  image_var=PROXYLOOM_${service}_IMAGE
  manifest_var=PROXYLOOM_${service}_MANIFEST_DIGEST
  config_var=PROXYLOOM_${service}_CONFIG_DIGEST
  [[ ${!manifest_var} =~ ^sha256:[0-9a-f]{64}$ && ${!config_var} =~ ^sha256:[0-9a-f]{64}$ ]] || exit 1
  image_id=$(docker image inspect --format '{{.Id}}' "${!image_var}")
  # Docker's classic store identifies images by config digest; the containerd
  # store uses the manifest digest. Both expected values come from the same
  # checked release archive. Resolve the tag once, then use only that exact ID.
  [[ $image_id == "${!manifest_var}" || $image_id == "${!config_var}" ]] || { echo 'Release image digest mismatch.' >&2; exit 1; }
  printf -v "$image_var" '%s' "$image_id"
  export "$image_var"
done
command=${1:-status}
shift || true

load_state() {
  [[ -r $state/secrets/deployment.env && -r $state/identity.path ]] || { echo 'Initialize this state directory first.' >&2; exit 1; }
  export PROXYLOOM_IDENTITY_DIR
  PROXYLOOM_IDENTITY_DIR=$(cat "$state/identity.path")
  [[ $PROXYLOOM_IDENTITY_DIR == "$state/"* && -r $PROXYLOOM_IDENTITY_DIR/runner.env ]] || exit 1
  # Use Compose's dotenv parser; do not execute state files as shell code.
  runner_id=$(sed -n 's/^PROXYLOOM_RUNNER_ID=//p' "$PROXYLOOM_IDENTITY_DIR/runner.env")
  [[ $runner_id =~ ^[a-f0-9-]{36}$ ]] || exit 1
}
dc() { docker compose --env-file "$state/secrets/deployment.env" --env-file "$PROXYLOOM_IDENTITY_DIR/runner.env" -f "$release/compose.yaml" "$@"; }
initialize() {
  local public_url=$1
  [[ ! -e $state || -d $state && -z $(find "$state" -mindepth 1 -maxdepth 1 -print -quit) ]] || { echo 'Initialization requires a new empty state directory.' >&2; exit 1; }
  mkdir -p -- "$state"
  chmod 700 "$state"
  docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 0:0 \
    --mount "type=bind,src=$state,dst=/state" "$PROXYLOOM_API_IMAGE" admin init-deployment --directory /state/secrets --architecture "$arch" --public-url "$public_url"
  mkdir -p -- "$state/old-master-keys" "$state/network-guard" "$state/backups" "$state/restore-work"
  chmod 755 "$state/old-master-keys" "$state/network-guard"
  chown 10001:10001 "$state/backups" "$state/restore-work"
  printf '%s\n' "$state/secrets" > "$state/identity.path"
  load_state
}
guard() {
  local runner api
  runner=$(dc ps -q runner); api=$(dc ps -q api)
  [[ -n $runner && -n $api ]] || { echo 'API and Runner must be running.' >&2; return 1; }
  bash "$release/network-guard.sh" "$runner" "$api" "$runner_id" "$state/network-guard"
}
start() {
  dc up -d --wait postgres
  dc run --rm --no-deps migrate migrate up
  dc up -d --no-deps --wait api
  dc up -d --no-deps runner
  guard
  dc up -d --no-deps --wait --wait-timeout 120 runner
}
backup() { dc run --rm --no-deps operations admin backup --directory /backups; }
stage_identity() {
  # The offline identity can remain host-root-only. Give the one-shot operation
  # its own private, readable copy and remove exactly that file on exit.
  temporary_identity=$(mktemp "$state/restore-work/identity-XXXXXX")
  trap 'rm -f -- "$temporary_identity"' EXIT
  cp -- "$1" "$temporary_identity"
  chown 10001:10001 "$temporary_identity"
  chmod 400 "$temporary_identity"
}

case $command in
  init)
    [[ $# == 1 ]] || { echo 'init PUBLIC_URL' >&2; exit 1; }
    initialize "$1"
    echo 'Initialized. Preserve an independent key export, then run start and install-timers.'
    ;;
  restore)
    [[ $# == 3 ]] || { echo 'restore ARCHIVE KEY_EXPORT_DIRECTORY PUBLIC_URL (new empty state only)' >&2; exit 1; }
    [[ $1 == /* && $2 == /* ]] || { echo 'Restore paths must be absolute.' >&2; exit 1; }
    archive=$(realpath "$1"); keydir=$(realpath "$2")
    [[ -f $archive && -d $keydir && -r $keydir/master-key-id && -r $keydir/backup_identity ]] || exit 1
    key_id=$(cat "$keydir/master-key-id")
    [[ $key_id =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || exit 1
    for key in master_key token_pepper content_hmac_key old_master_keys; do [[ -f $keydir/$key && ! -L $keydir/$key ]] || exit 1; done
    initialize "$3"
    for key in master_key token_pepper content_hmac_key old_master_keys master-key-id; do cp -- "$keydir/$key" "$state/secrets/$key"; chmod 444 "$state/secrets/$key"; done
    if [[ -d $keydir/old-master-keys ]]; then cp -a -- "$keydir/old-master-keys/." "$state/old-master-keys/"; fi
    sed -i "s/^PROXYLOOM_MASTER_KEY_ID=.*/PROXYLOOM_MASTER_KEY_ID=$key_id/" "$state/secrets/deployment.env"
    dc up -d --wait postgres
    stage_identity "$keydir/backup_identity"
    # Atomic restore refuses a nonempty database, upgrades compatible schemas and
    # revokes old authorization before committing. A failed attempt stays offline.
    dc run --rm --no-deps -v "$archive:/restore-input:ro" -v "$temporary_identity:/restore-identity:ro" operations \
      admin restore-backup --input /restore-input --identity-file /restore-identity > "$state/restore-receipt.json"
    epoch=$(sed -n 's/.*"runner_authorization_epoch":"\([a-f0-9-]*\)".*/\1/p' "$state/restore-receipt.json")
    [[ $epoch =~ ^[a-f0-9-]{36}$ ]] || exit 1
    identity="$state/identity-$epoch"
    docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges --user 0:0 \
      --mount "type=bind,src=$state,dst=/state" "$PROXYLOOM_API_IMAGE" admin init-runner-identity --directory "/state/identity-$epoch" --architecture "$arch" --authorization-epoch "$epoch"
    printf '%s\n' "$identity" > "$state/identity.path"
    load_state
    echo 'Restore completed with new authorization and Runner identity. Run start, then log in again.'
    ;;
  *)
    load_state
    case $command in
      start) start;;
      stop) dc stop runner api postgres;;
      status) dc ps;;
      guard) guard;;
      backup) backup;;
      verify-backup)
        [[ $# == 2 ]] || { echo 'verify-backup ARCHIVE IDENTITY_FILE' >&2; exit 1; }
        [[ $1 == /* && $2 == /* ]] || { echo 'Backup paths must be absolute.' >&2; exit 1; }
        archive=$(realpath "$1"); identity=$(realpath "$2")
        stage_identity "$identity"
        dc run --rm --no-deps -v "$archive:/restore-input:ro" -v "$temporary_identity:/restore-identity:ro" operations admin verify-backup --input /restore-input --identity-file /restore-identity
        ;;
      export-keys)
        [[ $# == 1 && $1 == /* && ! -e $1 ]] || { echo 'export-keys NEW_ABSOLUTE_DIRECTORY' >&2; exit 1; }
        mkdir -m 700 -- "$1"
        for key in master_key master-key-id token_pepper content_hmac_key old_master_keys backup_identity; do cp -- "$state/secrets/$key" "$1/$key"; done
        cp -a -- "$state/old-master-keys" "$1/old-master-keys"
        echo 'Independent key export created. Store it separately from database backups.'
        ;;
      upgrade) backup; dc stop runner api; start;;
      rollback)
        status=$(dc run --rm --no-deps migrate migrate status)
        [[ $status == *'"current":true'* ]] || { echo 'Image is incompatible with the current schema.' >&2; exit 1; }
        dc stop runner api; start
        ;;
      install-timers)
        [[ $release =~ ^/[A-Za-z0-9_./-]+$ ]] || { echo 'Systemd installation requires a path without spaces.' >&2; exit 1; }
        for job in guard backup; do
          unit="proxyloom-${PROXYLOOM_PROJECT:-proxyloom}-$job"
          [[ $unit =~ ^[A-Za-z0-9_-]+$ ]] || exit 1
          cat > "/etc/systemd/system/$unit.service" <<EOF
[Unit]
Description=ProxyLoom $job
After=docker.service network-online.target
Requires=docker.service
[Service]
Type=oneshot
Environment=PROXYLOOM_STATE_DIR=$state
Environment=PROXYLOOM_PROJECT=${PROXYLOOM_PROJECT:-proxyloom}
ExecStart=/bin/bash $release/proxyloom.sh $job
TimeoutStartSec=2h
EOF
          if [[ $job == guard ]]; then schedule=$'OnBootSec=30s\nOnUnitActiveSec=30s'; else schedule=$'OnCalendar=*-*-* 00:00:00 UTC\nPersistent=true'; fi
          printf '[Unit]\nDescription=ProxyLoom %s schedule\n[Timer]\n%s\nUnit=%s.service\n[Install]\nWantedBy=timers.target\n' "$job" "$schedule" "$unit" > "/etc/systemd/system/$unit.timer"
          chmod 644 "/etc/systemd/system/$unit.service" "/etc/systemd/system/$unit.timer"
          systemctl daemon-reload
          systemctl enable --now "$unit.timer"
        done
        ;;
      *) echo 'Commands: init, start, stop, status, guard, backup, verify-backup, export-keys, restore, upgrade, rollback, install-timers' >&2; exit 1;;
    esac
    ;;
esac
