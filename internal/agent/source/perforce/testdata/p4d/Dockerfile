# syntax=docker/dockerfile:1
FROM debian:bookworm-slim

ARG P4D_VERSION=r25.2
ARG P4_BIN_BASE=https://ftp.perforce.com/perforce/${P4D_VERSION}/bin.linux26x86_64

RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates bash \
 && rm -rf /var/lib/apt/lists/*

# Standalone p4d / p4 binaries are published as bare executables in this directory in r25.2.
RUN curl -fsSL -o /usr/local/bin/p4d "${P4_BIN_BASE}/p4d" \
 && curl -fsSL -o /usr/local/bin/p4  "${P4_BIN_BASE}/p4" \
 && chmod +x /usr/local/bin/p4d /usr/local/bin/p4

RUN useradd -m -d /home/perforce -s /bin/bash perforce \
 && mkdir -p /var/p4root \
 && chown perforce:perforce /var/p4root

COPY --chown=perforce:perforce entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER perforce
WORKDIR /home/perforce
EXPOSE 1666

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
