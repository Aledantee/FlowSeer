# FlowSeer t1 RESTCONF reference server: stock clixon-example with the
# RESTCONF daemon actually started (the upstream image only starts the
# backend).
# The digest is what the `latest` tag resolved to on 2026-10-06.
FROM clixon/clixon-example@sha256:c2f2bc4e4b7181dfdf1e3d5cc4ade45cd1af265e9ac43a47051bfea0c2bf5e58

COPY startup.sh /flowseer-startup.sh
RUN chmod +x /flowseer-startup.sh
CMD ["/flowseer-startup.sh"]
