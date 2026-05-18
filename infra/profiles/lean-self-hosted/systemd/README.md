# Systemd units (binaries on the host, no containers)

The `codereviewer.service` unit installed by the Terraform user-data
runs the full docker-compose stack. If you'd rather run the Go binaries
directly on the host — closer-to-the-metal latency, no docker hop,
simpler resource accounting — use these unit files instead.

## One-time host prep

```sh
# system user, owns config + state dir
sudo useradd --system --create-home --home-dir /var/lib/codereviewer \
  --shell /usr/sbin/nologin codereviewer

sudo install -d -m 0750 -o codereviewer -g codereviewer /etc/codereviewer
sudo install -d -m 0750 -o codereviewer -g codereviewer /var/lib/codereviewer
sudo install -d -m 0750 -o codereviewer -g codereviewer /var/lib/codereviewer/exports

# drop config.toml + env file (chmod 0640, owner codereviewer:codereviewer)
sudo -u codereviewer cp /path/to/your/config.toml /etc/codereviewer/config.toml
sudo -u codereviewer cp /path/to/your/env         /etc/codereviewer/env

# install the binaries (from the release tarball, or `go build` output)
sudo install -m 0755 ./codereviewer-webhook-gateway /usr/local/bin/
sudo install -m 0755 ./codereviewer-review-worker   /usr/local/bin/
sudo install -m 0755 ./codereviewer-indexer-worker  /usr/local/bin/
sudo install -m 0755 ./codereviewer-feedback-worker /usr/local/bin/
sudo install -m 0755 ./codereviewer-admin-ui        /usr/local/bin/
```

## Install the units

```sh
sudo install -m 0644 *.service /etc/systemd/system/
sudo install -m 0644 codereviewer.target /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now codereviewer.target
sudo systemctl status codereviewer.target
```

## Logs

All units log to the journal:

```sh
journalctl -u codereviewer-review-worker -f
journalctl -u codereviewer-webhook-gateway --since '5 min ago'
```

The application also writes structured JSON to stdout — the journal
captures it verbatim; `journalctl -o json-pretty` parses it. The
payload scrubber in `obsstdout` already strips diff/code-shaped values
before they reach the journal, so this remains safe to share.

## Differences from the docker-compose path

| | docker-compose unit | per-binary units |
|---|---|---|
| Postgres | `pgvector/pgvector:pg16` container | host-installed `postgresql-16` + `pgvector` package |
| NATS | `nats:2-alpine` container | host-installed `nats-server` |
| LiteLLM | sidecar container | upstream `litellm` service or skip (point `LlmGateway` at the provider directly) |
| Process management | one service starts the whole stack | one unit per binary, finer restart granularity |
| Resource limits | container `mem_limit` etc. | `MemoryMax=`, `CPUQuota=` in each unit file |

Either path works; pick whichever your ops org is more comfortable
operating.
