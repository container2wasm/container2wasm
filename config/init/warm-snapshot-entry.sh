#!/bin/sh
# Build-time warm snapshot entrypoint (C2W_WARM_SNAPSHOT=1).
#
# The Bochs WASI build (wasm.cc console_write) watches the guest console output
# for 10 consecutive '=' characters. When it sees them it sets BXPN_WASM_INITDONE,
# which makes the emulator unwind out of bxmain so Wizer can take the snapshot.
# The "==========" itself is swallowed (never printed to the user).
#
# So we print exactly that marker once the container is up, then exec an
# interactive bash that blocks reading the console -- mirroring /sbin/init's own
# `printf("==========")` + stdin-read pattern. Wizer captures the snapshot with
# bash ready and waiting for input; on resume the user lands straight in bash.
set -eu
export HOME=/root
export PS1='root@localhost:~# '
cd "$HOME" 2>/dev/null || cd /
# Emit the 10-'=' marker that Bochs (wasm.cc) watches for: it sets wasm.initdone
# and the CPU loop unwinds out of bxmain so Wizer snapshots right here.
printf '=========='
# On resume Bochs injects the "=\n" handshake into the first stdin read; consume
# it here so the interactive bash below starts on a clean prompt.
read _c2w_handshake 2>/dev/null || true
exec /bin/bash -i
