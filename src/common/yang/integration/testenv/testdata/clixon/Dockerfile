# FlowSeer t1 RESTCONF reference server: stock clixon-example with the
# RESTCONF daemon actually started (the upstream image only starts the
# backend).
FROM clixon/clixon-example:latest

COPY startup.sh /flowseer-startup.sh
RUN chmod +x /flowseer-startup.sh
CMD ["/flowseer-startup.sh"]
