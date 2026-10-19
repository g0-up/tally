# wal-g sidecar of the production database (infra/docker-compose.backup.yml): ships the WAL that
# postgres drops in the spool volume and takes the nightly base backup. Ubuntu 24.04 matches the
# glibc wal-g release asset; postgres itself stays on postgres:16-alpine.
FROM ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac

ARG TARGETARCH=amd64
ARG WALG_VERSION=v3.0.9
ARG WALG_SHA256_AMD64=8015836f246a978f27b31a2462eac7765312558ae5b11ac033322b555abf9e22
ARG WALG_SHA256_ARM64=1edd72c3387bfc3a1c19acad9daa36709dbb7cff0642f7044e175d5f8b555d64

RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) asset=wal-g-pg-24.04-amd64; sum="${WALG_SHA256_AMD64}" ;; \
      arm64) asset=wal-g-pg-24.04-aarch64; sum="${WALG_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl postgresql-client-16 tzdata; \
    curl -fsSLo /usr/local/bin/wal-g "https://github.com/wal-g/wal-g/releases/download/${WALG_VERSION}/${asset}"; \
    echo "${sum}  /usr/local/bin/wal-g" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/wal-g; \
    apt-get purge -y curl; \
    apt-get autoremove -y; \
    rm -rf /var/lib/apt/lists/*; \
    wal-g --version; \
    # wal-g looks the running uid up and fails without a passwd entry ("unknown userid 70").
    groupadd -g 70 postgres; \
    useradd -u 70 -g 70 -d /var/lib/walg -M -s /usr/sbin/nologin -K UID_MIN=1 postgres; \
    # A new named volume takes the owner of its mount point, so the spool is writable by postgres
    # before the sidecar's own preparation has run.
    install -d -m 0700 -o 70 -g 70 /var/lib/postgresql/wal-spool /var/lib/walg

COPY walg-sidecar.sh /usr/local/bin/walg-sidecar
RUN chmod 0755 /usr/local/bin/walg-sidecar

# Runs as the postgres uid/gid of postgres:16-alpine (70), which owns the data volume; the spool
# and state volumes take their owner from the directories above. Started as root instead
# (`--user 0`), the sidecar fixes the owner of both volumes and drops to 70 itself.
USER postgres
ENTRYPOINT ["/usr/local/bin/walg-sidecar"]
CMD ["run"]
