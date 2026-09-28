# Deploy FiveAgent on a Linux server

The Linux instance is the sandbox twin of the Windows PC: WhatsApp stays
on the PC, the Linux server talks over Telegram (long polling - no
public URL needed) and runs the bubblewrap sandbox. Tested in CI on
ubuntu-latest on every push.

## Paso 1: install bubblewrap

```
sudo apt update && sudo apt install -y bubblewrap
```

You should see: `Setting up bubblewrap ...` and `bwrap --version` prints
a version.

## Paso 2: allow unprivileged user namespaces (Ubuntu 24.04+ only)

```
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
echo 'kernel.apparmor_restrict_unprivileged_userns=0' | sudo tee /etc/sysctl.d/90-fiveagent.conf
```

You should see: the sysctl line printed back. Without it bwrap fails
with `Failed RTM_NEWADDR`.

## Paso 3: get the binary

On the server (needs Go 1.24+):

```
git clone https://github.com/FiveTechSoft/FiveAgent.git
cd FiveAgent
go build -o fiveagent ./app/fiveagent
```

Or cross-compile from the Windows PC (git-bash or WSL) and copy:

```
sh deploy/build-linux.sh
scp fiveagent-linux-amd64 user@server:~/FiveAgent/fiveagent
```

You should see: `Built fiveagent-linux-amd64` / a `fiveagent` binary
(`file fiveagent` says ELF 64-bit).

## Paso 4: configure

```
cp fiveagent.yml.example fiveagent.yml
```

Edit fiveagent.yml - the minimum for the Linux twin:

```
model:
  base_url: http://localhost:11434/v1   # Ollama on this server, or a reachable one
  api_key: ""
  name: qwen3.5:9b
channels:
  telegram:
    enabled: true
    bot_token: "123456:ABC..."          # from @BotFather on Telegram
    allowed_senders: ["123456789"]      # your Telegram chat ID
memory:
  knowledge: data/memory
  json: fiveagent-memory.json           # simplest store; or postgres like the PC
sandbox:
  enabled: true
```

Why Telegram: the WhatsApp webhook points at the Windows PC (Meta
delivers to one URL only), while Telegram long polling needs no public
endpoint - perfect for a headless server.

## Paso 5: run

Quick start (tmux):

```
tmux new -s fiveagent
./fiveagent
```

As a service:

```
sudo mkdir -p /opt/fiveagent
sudo cp fiveagent fiveagent.yml /opt/fiveagent/
sudo cp deploy/fiveagent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fiveagent
```

You should see: `systemctl status fiveagent` says active (running), and
`journalctl -u fiveagent -f` shows `sandbox: bubblewrap backend` and
`long-term memory: data/memory`.

## Paso 6: verify the sandbox for real

Send the Telegram bot: `ejecuta el comando ls`. You should get a file
listing - those files live under `data/sandbox/` on the server, and the
command has no network access and cannot see the host filesystem.

If the log says `sandbox disabled: ...`, that line names the cause
(usually Paso 1 or 2).
