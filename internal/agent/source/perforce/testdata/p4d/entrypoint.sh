#!/usr/bin/env bash
# Entrypoint for the p4d test container.
# Runs as the unprivileged 'perforce' user (set in the Dockerfile via USER).
set -euo pipefail

P4ROOT=/var/p4root

echo "[entrypoint] starting p4d..."
# `p4d -r ROOT` sets the database root. NOTE: `-d` means "daemonize" in
# p4d (not "set root" as in some other servers); using -d here would
# detach p4d and exit immediately because $P4ROOT is then a positional
# arg, not a flag value.
p4d -r "$P4ROOT" -p 0.0.0.0:1666 &
P4D_PID=$!

# Talk to local p4d on loopback during setup.
export P4PORT=localhost:1666
export P4USER=perforce

echo "[entrypoint] waiting for p4d to respond..."
for _ in $(seq 1 30); do
  if p4 info >/dev/null 2>&1; then
    echo "[entrypoint] p4d responsive"
    break
  fi
  sleep 1
done
if ! p4 info >/dev/null 2>&1; then
  echo "[entrypoint] FATAL: p4d did not respond within 30s" >&2
  exit 1
fi

echo "[entrypoint] disabling auth (security=0)..."
p4 configure set security=0 >/dev/null

echo "[entrypoint] creating depot //test ..."
# Stream depots in p4d r25.x require the `StreamDepth` field; without it
# the form is rejected with "type stream requires StreamDepth field".
p4 depot -i <<'EOF'
Depot:        test
Owner:        perforce
Type:         stream
StreamDepth:  //test/1
Map:          test/...
EOF

echo "[entrypoint] creating stream //test/main ..."
# Mainline streams in p4d r25.x require the `ParentView` field even when
# Parent is `none`; `noinherit` is the conventional value for a mainline
# without an inherited parent view.
p4 stream -i <<'EOF'
Stream:      //test/main
Owner:       perforce
Name:        main
Parent:      none
Type:        mainline
ParentView:  noinherit
Paths:       share ...
EOF

WORKDIR=$(mktemp -d)
echo "[entrypoint] creating setup client rooted at ${WORKDIR} ..."
p4 client -i <<EOF
Client:   setup-client
Owner:    perforce
Root:     ${WORKDIR}
Stream:   //test/main
EOF
export P4CLIENT=setup-client

echo "[entrypoint] populating //test/main with baseline file ..."
echo "baseline" > "${WORKDIR}/readme.txt"
p4 add "${WORKDIR}/readme.txt"
p4 submit -d "init"

echo "[entrypoint] creating shelved CL ..."
SHELVED_CL=$(p4 --field "Description=relay-test-shelf" change -o | p4 change -i | awk '{print $2}')
p4 edit -c "$SHELVED_CL" "${WORKDIR}/readme.txt"
echo "shelved-content" > "${WORKDIR}/readme.txt"
p4 shelve -c "$SHELVED_CL"
# Revert the local-side opens; the shelf remains on the server.
p4 revert -k "${WORKDIR}/readme.txt"

echo "$SHELVED_CL" > /var/p4root/shelved-cl.txt
echo "[entrypoint] shelved CL = $SHELVED_CL"

echo "p4d ready"

wait "$P4D_PID"
