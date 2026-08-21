#!/bin/sh
# Start the clixon backend, then the RESTCONF frontend, and hold the
# container open. The example config serves RESTCONF on :80 with
# auth-type none and answers /.well-known/host-meta.
set -e
/usr/local/sbin/clixon_backend -s running -l e -f /usr/local/etc/clixon/example.xml
echo "clixon backend started"
/usr/local/sbin/clixon_restconf -l e -f /usr/local/etc/clixon/example.xml &
echo "clixon restconf started"
exec /bin/sleep 100000000
