# FlowSeer t1 NETCONF reference server: stock netopeer2/sysrepo with
# the yanggen fixture modules installed, so integration tests exercise
# the committed generated fixture bindings against a real server.
# The digest is what the `latest` tag resolved to on 2026-10-06.
FROM sysrepo/sysrepo-netopeer2@sha256:36cc1d841c97f118d62f775024d60fb1ce210c3bca6b32135c1a9e5a358a1cc9

COPY fixture-types.yang fixture-grp.yang fixture-main-sub.yang fixture-main.yang fixture-aug.yang /fixtures/

# Install in dependency order; the submodule rides along via the
# search dir. fixture-dev (the deviation module) is deliberately not
# installed: the server should accept the un-deviated tree the
# bindings model.
RUN sysrepoctl -s /fixtures -i /fixtures/fixture-types.yang && \
    sysrepoctl -s /fixtures -i /fixtures/fixture-grp.yang && \
    sysrepoctl -s /fixtures -i /fixtures/fixture-main.yang && \
    sysrepoctl -s /fixtures -i /fixtures/fixture-aug.yang

# The image's NACM default denies writes for the netconf user; the t1
# tier exists to exercise the edit path, so disable NACM in both
# startup and running.
RUN echo '<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><enable-nacm>false</enable-nacm></nacm>' > /tmp/nacm.xml && \
    sysrepocfg --edit=/tmp/nacm.xml -d startup -m ietf-netconf-acm && \
    sysrepocfg --edit=/tmp/nacm.xml -d running -m ietf-netconf-acm
