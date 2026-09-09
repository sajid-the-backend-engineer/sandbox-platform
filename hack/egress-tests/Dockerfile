# Copyright © 2026 Northrays Private Limited
# SPDX-License-Identifier: AGPL-3.0

# Environment for the runner's privileged egress tests.
#
# The tests install real iptables rules and drive a real Docker daemon, so they need
# NET_ADMIN and a daemon of their own. Running them against the CI host's daemon would
# work but would leave firewall rules on a shared machine; a nested daemon in a
# throwaway privileged container keeps the blast radius inside the container.
#
# docker.io on Debian ships only the daemon -- docker-cli is a separate package, and
# omitting it leaves `docker` missing while dockerd runs perfectly, which is a
# confusing way to fail.
FROM golang:1.25

RUN apt-get update && apt-get install -y --no-install-recommends \
      docker.io docker-cli iptables ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY run.sh /usr/local/bin/run-egress-tests
RUN chmod +x /usr/local/bin/run-egress-tests

ENTRYPOINT ["/usr/local/bin/run-egress-tests"]
