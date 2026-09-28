#!/bin/sh
# Cross-compile FiveAgent for Linux (amd64) from any dev machine.
# On Windows run it from git-bash or WSL. Produces fiveagent-linux-amd64.
set -e
cd "$(dirname "$0")/.."
GOOS=linux GOARCH=amd64 go build -o fiveagent-linux-amd64 ./app/fiveagent
echo "Built fiveagent-linux-amd64"
echo "Copy it: scp fiveagent-linux-amd64 user@server:/opt/fiveagent/fiveagent"
