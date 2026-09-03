# FlowSeer SNMP integration T3 — lextudio/snmpsim replay engine.
#
# Built from python:3.12-slim with the snmpsim-lextudio package
# installed. The container's entrypoint runs snmpsim-command-responder
# on UDP/1024 (unprivileged); testcontainers maps that to a random
# host port. The replay data dir is mounted in via the testcontainers
# orchestration in testenv/snmpsim.go.
#
# Multi-arch: builds clean on linux/amd64 and linux/arm64 (Apple
# Silicon, AWS Graviton, etc.) because the Python base is multi-arch
# and snmpsim-lextudio is pure Python.

FROM python:3.12-slim

RUN pip install --no-cache-dir snmpsim-lextudio

# snmpsim-command-responder refuses to run as root (it requires a
# non-privileged process-user / process-group). Pre-create a dedicated
# user; snmpsim drops to it via --process-user / --process-group.
RUN groupadd --system snmpsim && \
    useradd  --system --gid snmpsim --home /usr/local/snmpsim snmpsim && \
    mkdir -p /usr/local/snmpsim/data && \
    chown -R snmpsim:snmpsim /usr/local/snmpsim

EXPOSE 1024/udp

CMD ["snmpsim-command-responder", \
     "--data-dir=/usr/local/snmpsim/data", \
     "--agent-udpv4-endpoint=0.0.0.0:1024", \
     "--process-user=snmpsim", \
     "--process-group=snmpsim"]
