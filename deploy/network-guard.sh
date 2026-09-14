#!/usr/bin/env bash
# Called by proxyloom.sh with the exact container IDs owned by its Compose project.
set -euo pipefail
[[ $EUID == 0 && $# == 4 ]] || { echo 'network_guard_requires_root_and_container_ids' >&2; exit 1; }
runner=$1 api=$2 runner_id=$3 receipt_dir=$4
[[ $runner =~ ^[a-f0-9]{64}$ && $api =~ ^[a-f0-9]{64}$ && $runner_id =~ ^[a-f0-9-]{36}$ && $receipt_dir == /* ]] || exit 1
for tool in nsenter iptables ip6tables ip; do command -v "$tool" >/dev/null; done
pid=$(docker inspect --format '{{.State.Pid}}' "$runner")
[[ $pid =~ ^[0-9]+$ && $pid -gt 0 ]] || { echo 'network_guard_runner_not_running' >&2; exit 1; }
namespace=$(readlink "/proc/$pid/ns/net")
boot=$(cat /proc/sys/kernel/random/boot_id)
startup_nonce=''
for ((attempt=0; attempt<100; attempt++)); do
  if candidate_nonce=$(docker exec "$runner" cat /tmp/proxyloom-network-guard-nonce 2>/dev/null) && [[ $candidate_nonce =~ ^[0-9a-f]{64}$ ]]; then startup_nonce=$candidate_nonce; break; fi
  sleep 0.1
done
[[ $startup_nonce =~ ^[0-9a-f]{64}$ ]] || { echo 'network_guard_startup_nonce_missing' >&2; exit 1; }
# The control network is the one shared by API and Runner. Neither data nor front
# is attached to Runner; egress is never attached to API.
api_ip=''
while IFS= read -r network; do
  candidate=$(docker inspect --format "{{with index .NetworkSettings.Networks \"$network\"}}{{.IPAddress}}{{end}}" "$api")
  if [[ -n $candidate ]]; then
    [[ -z $api_ip ]] || { echo 'network_guard_ambiguous_control_network' >&2; exit 1; }
    api_ip=$candidate
  fi
done < <(docker inspect --format '{{range $name, $value := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$runner")
[[ $api_ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1
receipt=$(printf '{"runner_id":"%s","boot_id":"%s","network_namespace":"%s","startup_nonce":"%s"}' "$runner_id" "$boot" "$namespace" "$startup_nonce")
# Rules live in this disposable namespace. Never change the host firewall.
# Verify the chain before accepting a cached receipt (e.g. after manual repair).
if [[ -f $receipt_dir/ready.json && $(cat "$receipt_dir/ready.json") == "$receipt" ]] &&
  nsenter -t "$pid" -n iptables -w -C OUTPUT -j PROXYLOOM_OUT 2>/dev/null &&
  nsenter -t "$pid" -n iptables -w -C PROXYLOOM_OUT -d "$api_ip" -p tcp --dport 9091 -j ACCEPT 2>/dev/null &&
  nsenter -t "$pid" -n ip6tables -w -C OUTPUT -j PROXYLOOM_OUT 2>/dev/null; then exit 0; fi
# Set a temporary OUTPUT drop before rebuilding. An existing Runner can only lose
# traffic, never gain an unguarded interval. Local SOCKS traffic resumes afterwards.
for family in iptables ip6tables; do
  nsenter -t "$pid" -n "$family" -w -I OUTPUT 1 -j DROP
  nsenter -t "$pid" -n "$family" -w -N PROXYLOOM_OUT 2>/dev/null || true
  nsenter -t "$pid" -n "$family" -w -F PROXYLOOM_OUT
  nsenter -t "$pid" -n "$family" -w -A PROXYLOOM_OUT -o lo -j ACCEPT
done
nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -d "$api_ip" -p tcp --dport 9091 -j ACCEPT
# System settings authorize individual private endpoints in every frozen job
# and Runner rechecks that authorization. The network namespace keeps its port
# and all other protected-address boundaries; it must admit RFC1918 here so an
# administrator can update the self-hosted allowlist without a host restart.
for prefix in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16; do
  nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -d "$prefix" -p tcp -j ACCEPT
done
for prefix in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 192.0.0.0/24 224.0.0.0/4 240.0.0.0/4; do
  nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -d "$prefix" -j REJECT
done
# Block the host's public addresses too. A public address must not bypass the
# protected-host boundary merely because it is outside RFC1918.
while IFS= read -r address; do
  [[ -z $address ]] || nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -d "$address" -j REJECT
done < <(ip -4 -o address show scope global | awk '{split($4,a,"/"); print a[1]}')
nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -p tcp -j ACCEPT
nsenter -t "$pid" -n iptables -w -A PROXYLOOM_OUT -j REJECT
for prefix in ::/128 ::1/128 fc00::/7 fe80::/10 ff00::/8 2001:db8::/32; do
  nsenter -t "$pid" -n ip6tables -w -A PROXYLOOM_OUT -d "$prefix" -j REJECT
done
while IFS= read -r address; do
  [[ -z $address ]] || nsenter -t "$pid" -n ip6tables -w -A PROXYLOOM_OUT -d "$address" -j REJECT
done < <(ip -6 -o address show scope global | awk '{split($4,a,"/"); print a[1]}')
nsenter -t "$pid" -n ip6tables -w -A PROXYLOOM_OUT -p tcp -j ACCEPT
nsenter -t "$pid" -n ip6tables -w -A PROXYLOOM_OUT -j REJECT
for family in iptables ip6tables; do
  nsenter -t "$pid" -n "$family" -w -C OUTPUT -j PROXYLOOM_OUT 2>/dev/null || nsenter -t "$pid" -n "$family" -w -A OUTPUT -j PROXYLOOM_OUT
  # Only remove our exact temporary rule; never flush an unrelated chain.
  while nsenter -t "$pid" -n "$family" -w -C OUTPUT -j DROP 2>/dev/null; do nsenter -t "$pid" -n "$family" -w -D OUTPUT -j DROP; done
done
[[ $(docker inspect --format '{{.State.Pid}}' "$runner") == "$pid" && $(readlink "/proc/$pid/ns/net") == "$namespace" ]] || exit 1
[[ $(docker exec "$runner" cat /tmp/proxyloom-network-guard-nonce) == "$startup_nonce" ]] || exit 1
umask 022
printf '%s\n' "$receipt" > "$receipt_dir/ready.json.new"
chmod 444 "$receipt_dir/ready.json.new"
mv -f -- "$receipt_dir/ready.json.new" "$receipt_dir/ready.json"
echo 'network_guard_ready'
