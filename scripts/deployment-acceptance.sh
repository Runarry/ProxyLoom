#!/usr/bin/env bash
# Runs exclusively inside the disposable Docker-in-Docker acceptance host.
set -euo pipefail
export PROXYLOOM_STATE_DIR=/state
cp -a /input/deploy /release
cp /input/images-amd64.env /release/images-amd64.env
cp /input/deployment-probe /probe
chmod 755 /probe
docker load -i /input/images-amd64.tar >/dev/null
operator() { bash /release/proxyloom.sh "$@"; }
dc() { docker compose --env-file /release/images-amd64.env --env-file /state/secrets/deployment.env --env-file /state/secrets/runner.env -f /release/compose.yaml "$@"; }
operator init http://127.0.0.1:8080
export PROXYLOOM_IDENTITY_DIR=/state/secrets
if [[ -r /input/legacy.env ]]; then
  docker load -i /input/legacy.tar >/dev/null
  source /input/legacy.env
  cat > /legacy-api.yaml <<'YAML'
services:
  api:
    environment:
      PROXYLOOM_RUNNER_LISTEN_ADDR: ''
      PROXYLOOM_RUNNER_CA_FILE: ''
      PROXYLOOM_RUNNER_CERT_FILE: ''
      PROXYLOOM_RUNNER_KEY_FILE: ''
      PROXYLOOM_RUNNER_REGISTRY_FILE: ''
YAML
  export PROXYLOOM_API_IMAGE=$LEGACY_API_IMAGE
  dc up -d --wait postgres
  legacy_status=$(dc run --rm --no-deps migrate migrate up)
  [[ $legacy_status == *'"latest":15'* && $legacy_status == *'"current":true'* ]]
  docker compose --env-file /release/images-amd64.env --env-file /state/secrets/deployment.env --env-file /state/secrets/runner.env -f /release/compose.yaml -f /legacy-api.yaml up -d --no-deps --wait api
  /probe init-legacy /state /probe-state.json
  unset PROXYLOOM_API_IMAGE
  operator upgrade
  /probe upgraded /state /probe-state.json
  echo 'PASS: actual-M2-schema-15-to-M3-upgrade'
else
  operator start
  /probe init /state /probe-state.json
fi
runner=$(dc ps -q runner); api=$(dc ps -q api); postgres=$(dc ps -q postgres)
[[ $(docker inspect --format '{{.Config.User}}' "$api") == 10001:10001 ]]
[[ $(docker inspect --format '{{.Config.User}}' "$runner") == 10002:10002 ]]
[[ $(docker inspect --format '{{len .HostConfig.PortBindings}}' "$runner") == 0 ]]
[[ $(docker inspect --format '{{len .HostConfig.PortBindings}}' "$postgres") == 0 ]]
[[ $(docker inspect --format '{{(index (index .HostConfig.PortBindings "8080/tcp") 0).HostIp}}' "$api") == 127.0.0.1 ]]
[[ $(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$runner") == true ]]
[[ $(docker inspect --format '{{len .NetworkSettings.Networks}}' "$runner") == 2 ]]
docker top "$postgres" -eo pid,user,comm | awk 'NR>1 && $3=="postgres" {if($2=="root")exit 1; found=1} END {if(!found)exit 1}'
echo 'PASS: production-service-layout-and-embedded-cores'

if [[ -r /input/legacy.env ]]; then
  # A genuinely different image ID with identical application bytes is the
  # compatible rollback fixture; the M2 API is the incompatible-schema fixture.
  # Neither fixture is included as a supported release image in the handoff.
  fixture=$(docker create --network none "$(docker inspect --format '{{.Image}}' "$api")")
  docker commit --change 'LABEL io.proxyloom.acceptance=compatible-schema-variant' "$fixture" proxyloom-compatible-fixture:local >/dev/null
  docker rm -v "$fixture" >/dev/null
  compatible_id=$(docker image inspect --format '{{.Id}}' proxyloom-compatible-fixture:local)
  for variant in compatible incompatible; do
    cp -a /release "/$variant"
    sed -i '/^PROXYLOOM_API_/d' "/$variant/images-amd64.env"
    if [[ $variant == compatible ]]; then tag=proxyloom-compatible-fixture:local; manifest=$compatible_id; config=$compatible_id
    else tag=$LEGACY_API_IMAGE; manifest=$LEGACY_API_MANIFEST_DIGEST; config=$LEGACY_API_CONFIG_DIGEST; fi
    printf 'PROXYLOOM_API_IMAGE=%s\nPROXYLOOM_API_MANIFEST_DIGEST=%s\nPROXYLOOM_API_CONFIG_DIGEST=%s\n' "$tag" "$manifest" "$config" >> "/$variant/images-amd64.env"
  done
  if bash /incompatible/proxyloom.sh rollback; then echo 'Incompatible M2 rollback accepted.' >&2; exit 1; fi
  /probe persisted /state /probe-state.json
  bash /compatible/proxyloom.sh rollback
  [[ $(docker inspect --format '{{.Image}}' "$(dc ps -q api)") == "$compatible_id" ]]
  /probe persisted /state /probe-state.json
  operator start
  /probe persisted /state /probe-state.json
  runner=$(dc ps -q runner); api=$(dc ps -q api); postgres=$(dc ps -q postgres)
  echo 'PASS: compatible-image-switch-and-incompatible-schema-rejection'
fi

# A recreated namespace cannot reuse the old receipt. Verify reachability on an
# actual listening private port before and after rule application, so a missing
# listener or invalid test command cannot produce a false positive.
old_nonce=$(docker exec "$runner" cat /tmp/proxyloom-network-guard-nonce)
dc up -d --no-deps --force-recreate runner
runner=$(dc ps -q runner)
pid=$(docker inspect --format '{{.State.Pid}}' "$runner")
for ((attempt=0; attempt<100; attempt++)); do
  if new_nonce=$(docker exec "$runner" cat /tmp/proxyloom-network-guard-nonce 2>/dev/null) && [[ $new_nonce =~ ^[0-9a-f]{64}$ ]]; then break; fi
  sleep 0.1
done
[[ $new_nonce != "$old_nonce" ]]
api_ip=$(docker inspect --format '{{(index .NetworkSettings.Networks "proxyloom_control").IPAddress}}' "$api")
nsenter -t "$pid" -n /probe dial "$api_ip:8080" allow
if docker exec "$runner" /app/proxyloom-runner healthcheck; then echo 'Stale guard enabled Runner.' >&2; exit 1; fi
operator guard
nsenter -t "$pid" -n /probe dial "$api_ip:8080" deny
nsenter -t "$pid" -n /probe dial "$api_ip:9091" allow
dc up -d --no-deps --wait runner
echo 'PASS: stale-guard-and-private-network-isolation'

dc restart postgres api runner
operator start
/probe persisted /state /probe-state.json
operator export-keys /key-export
if [[ -r /input/capacity-seconds ]]; then
  [[ $(docker inspect --format '{{.HostConfig.NanoCpus}}' "$(dc ps -q api)") == 2000000000 ]]
  [[ $(docker inspect --format '{{.HostConfig.Memory}}' "$(dc ps -q api)") == 536870912 ]]
  [[ $(docker inspect --format '{{.HostConfig.NanoCpus}}' "$(dc ps -q postgres)") == 1000000000 ]]
  [[ $(docker inspect --format '{{.HostConfig.NanoCpus}}' "$(dc ps -q runner)") == 2000000000 ]]
  cp /input/capacity-probe /capacity
  chmod 755 /capacity
  mkdir -p /results
  docker stats --format '{{json .}}' > /results/resources.jsonl &
  stats_pid=$!
  trap 'kill "$stats_pid" 2>/dev/null || true' EXIT
  postgres=$(dc ps -q postgres)
  db_ip=$(docker inspect --format '{{(index .NetworkSettings.Networks "proxyloom_data").IPAddress}}' "$postgres")
  GOMAXPROCS=2 /capacity /state /probe-state.json "$db_ip:5432" /results/capacity.json "$(cat /input/capacity-seconds)"
  kill "$stats_pid" 2>/dev/null || true
  wait "$stats_pid" 2>/dev/null || true
  trap - EXIT
fi
operator backup
archive=$(ls -1t /state/backups/*.age | head -n 1)
[[ -n $archive ]]
operator verify-backup "$archive" /key-export/backup_identity
# A missing application key must fail before creating a restore database.
cp -a /key-export /missing-key-export
rm -f -- /missing-key-export/content_hmac_key
if PROXYLOOM_PROJECT=proxyloom-missing-key PROXYLOOM_STATE_DIR=/missing-key-state operator restore "$archive" /missing-key-export http://127.0.0.1:8080; then
  echo 'Restore accepted an incomplete key export.' >&2; exit 1
fi
[[ ! -e /missing-key-state ]]
[[ -z $(docker ps -aq --filter label=com.docker.compose.project=proxyloom-missing-key) ]]
echo 'PASS: missing-key-restore-rejected-before-database-creation'
operator upgrade
/probe persisted /state /probe-state.json
operator rollback
/probe persisted /state /probe-state.json
operator stop
started=$(date +%s)
export PROXYLOOM_PROJECT=proxyloom-recovered PROXYLOOM_STATE_DIR=/recovered
operator restore "$archive" /key-export http://127.0.0.1:8080
operator start
/probe restored /recovered /probe-state.json
if [[ -r /input/capacity-seconds ]]; then
  postgres=$(docker ps -q --filter label=com.docker.compose.project=proxyloom-recovered --filter label=com.docker.compose.service=postgres)
  db_ip=$(docker inspect --format '{{(index .NetworkSettings.Networks "proxyloom-recovered_data").IPAddress}}' "$postgres")
  GOMAXPROCS=2 /capacity /recovered /probe-state.json "$db_ip:5432" /results/capacity.json restore
fi
elapsed=$(( $(date +%s) - started ))
[[ $elapsed -le 7200 ]]
printf 'PASS: isolated-restore-seconds=%s\n' "$elapsed"
echo 'PASS: production-deployment-acceptance'
