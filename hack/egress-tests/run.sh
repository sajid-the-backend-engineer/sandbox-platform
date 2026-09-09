#!/bin/sh
# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0
#
# Start a nested Docker daemon, then run the privileged egress suites against it.
set -e

dockerd --iptables=true >/var/log/dockerd.log 2>&1 &

# The daemon takes a few seconds. Poll rather than sleeping a fixed amount, and fail
# loudly with its log if it never arrives -- a test run against a missing daemon
# produces failures that look like enforcement bugs.
i=0
while [ $i -lt 60 ]; do
  docker info >/dev/null 2>&1 && break
  i=$((i + 1))
  sleep 1
done
if ! docker info >/dev/null 2>&1; then
  echo "dockerd did not become ready" >&2
  tail -40 /var/log/dockerd.log >&2
  exit 1
fi

echo "iptables backend: $(iptables -V)"
echo "docker: $(docker version --format '{{.Server.Version}}')"

# Pulled once here so a slow pull inside a test does not read as a timeout.
#
# Retried, because the registry is the one dependency in this run that fails for
# reasons that have nothing to do with the code under test -- a TLS handshake timeout
# to Docker Hub would otherwise report as a failure of the egress enforcement suites,
# which is exactly the wrong thing to tell somebody about a security control.
attempt=1
until docker pull -q alpine:3.20 >/dev/null 2>&1; do
  if [ $attempt -ge 4 ]; then
    echo "could not pull the probe image after $attempt attempts" >&2
    docker pull alpine:3.20 >&2 || true
    exit 1
  fi
  echo "probe image pull failed (attempt $attempt), retrying" >&2
  attempt=$((attempt + 1))
  sleep $((attempt * 5))
done

cd /src

# -p 1 is required, not tidiness. There is one kernel, and these two packages install
# and remove rules in the same tables; run in parallel they delete each other's rules
# and fail in whichever package loses the race.
exec go test -tags privileged -count=1 -p 1 -timeout 30m "$@" \
  ./apps/runner/pkg/egress/ ./apps/runner/pkg/docker/
