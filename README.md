# port-peeker

English | [日本語](README.ja.md)

A single-binary HTTP server that answers LB health probes by inspecting the host's TCP LISTEN state and the listening process. Returns 200/503/400 based on the query.

It reads `/proc` directly, so it does not generate connection logs against the target service and has no runtime dependency on tools like `ss`.

## Usage

```sh
# Start
port-peeker --listen :24365

# From an LB (or curl):
curl -s -o /dev/null -w '%{http_code}\n' \
  'http://127.0.0.1:24365/check?port=993&process=dovecot'
# → 200 (LISTEN and process name matches) / 503 (otherwise) / 400 (bad params)
```

## Endpoints

| Path | Purpose |
|---|---|
| `GET /check?port=N[&process=NAME]` | Verify that `port` is in LISTEN state (and optionally that the listening process name matches) |
| `GET /healthz` | Liveness of the agent itself (always 200) |

## Options

```
--listen ADDR         listen address (default ":24365")
--cache-ttl DURATION  TTL of /check result cache; 0 disables (default 5s)
--version             print version and exit
--help                show help
```

Running without arguments prints the same help as `--help`.

PROXY Protocol v1/v2 headers are auto-detected per connection, so `port-peeker` works behind an NLB with `proxy_protocol_v2 = ON`, behind HAProxy, or against direct plain HTTP probes — no flag to set.

## Requirements

- Linux (reads `/proc/net/tcp`, `/proc/net/tcp6`, `/proc/<pid>/fd`, `/proc/<pid>/comm`)
- Resolving process names of other UIDs requires the same UID or root. When running as a regular user, processes owned by other users show up as `(none)` and `process=` matching falls back to a 503.

## Build

```sh
just build           # host
just build-linux     # Linux amd64 + arm64
```

## Run as a systemd service

Use the bundled [`systemd/port-peeker.service`](systemd/port-peeker.service):

```sh
sudo install -m 755 bin/port-peeker-linux-arm64 /usr/local/bin/port-peeker
sudo install -m 644 systemd/port-peeker.service /etc/systemd/system/port-peeker.service
sudo systemctl daemon-reload
sudo systemctl enable --now port-peeker
curl -s http://127.0.0.1:24365/healthz
```

See [docs/design.md §5.3](docs/design.md) for details.

## Documentation

- [docs/design.md](docs/design.md) — Design document
- [docs/roadmap.md](docs/roadmap.md) — Future work
- [docs/decisions/](docs/decisions/) — Design Records (rationale of major decisions)

## License

MIT
