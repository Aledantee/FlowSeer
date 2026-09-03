# FlowSeer SNMP integration T1 — Net-SNMP snmpd reference agent.
#
# A thin layer over debian:bookworm-slim with snmpd, the FlowSeer T1
# snmpd.conf, and the forged-edge pass-protocol scripts. Built and
# orchestrated by src/common/snmp/test/integration/testenv/snmpd.go.
#
# The image runs snmpd in foreground with stdout logging so testcontainers'
# log-stream readiness checks see startup output. Container is ephemeral;
# USM state is reseeded from snmpd.conf on every start (createUser
# entries are processed at first boot of each container).
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        snmpd \
        snmp \
        libsnmp-base \
        && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

COPY snmpd.conf /etc/snmp/snmpd.conf
COPY pass-scripts/ /usr/local/share/snmp-pass/
RUN chmod 0755 /usr/local/share/snmp-pass/*.sh

# Persistent state dir for createUser usmUser entries.
RUN mkdir -p /var/lib/snmp

EXPOSE 161/udp

# Foreground (-f) so the container's lifetime is the agent's lifetime.
# Log to stdout (-Lo) for testcontainers stream capture.
# Note: snmpd's default config search includes /etc/snmp/snmpd.conf, so
# we do NOT pass -c — passing both -c and relying on the default path
# loads the file twice and triggers "duplicate registration" errors
# for every pass-script directive.
# Note: we do NOT drop privileges with -u / -g either. The container
# is ephemeral and isolated; running snmpd as root inside the container
# avoids the Debian-snmp UID/GID surface and the resulting "Error
# opening specified endpoint udp:161" failures observed when the
# privilege-drop sequence runs before bind.
CMD ["/usr/sbin/snmpd", "-f", "-Lo"]
