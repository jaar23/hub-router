# Observability

hub-router exposes three complementary observability interfaces:

| Interface | Path | Format | Auth |
|-----------|------|--------|------|
| Terminal dashboard | `-tui` flag | Live TUI | `HR_ADMIN_API_KEY` (env) |
| Stats endpoint | `GET /debug/stats` | JSON | `X-Admin-API-Key` header |
| Health check | `GET /health` | JSON | none |
| Prometheus metrics | `GET /metrics` | Prometheus text | `X-Admin-API-Key` header |

---

## Terminal Dashboard (TUI)

A live read-only dashboard that runs inside the container. It polls `/debug/stats` every second and renders a colour-coded view of queue activity, long-poll waiters, and security events.

### Launch

```bash
# Inside the running container
docker exec -it <container_name> /hub-router -tui

# If HR_ADMIN_API_KEY is set on the server, pass it via environment
docker exec -e HR_ADMIN_API_KEY=secret -it <container_name> /hub-router -tui

# Non-default port
docker exec -e HR_PORT=9090 -it <container_name> /hub-router -tui
```

The TUI reads the same environment variables as the server — no extra configuration:

| Variable | Purpose |
|----------|---------|
| `HR_PORT` | Port to connect to (default `8080`) |
| `HR_ADMIN_API_KEY` | Admin API key for `/debug/stats` auth |

### Screen layout

```
╭─ hub-router ────────────────────────── uptime: 1h23m45s ─╮
│                                                            │
│  ┌── QUEUE ──────────────────┐  ┌── STORE ─────────────┐  │
│  │ depth       42 / 10,000   │  │ results          7    │  │
│  │ enqueued    1,234  total  │  │ waiters         12    │  │
│  │ dequeued    1,190  total  │  └──────────────────────┘  │
│  │ expired         3  total  │                            │
│  │ dropped         0  total  │  ┌── SECURITY ──────────┐  │
│  └───────────────────────────┘  │ rate limiting   on    │  │
│                                 │ tracked IPs     15    │  │
│  ┌── THROUGHPUT ─────────────┐  │ throttled IPs    2    │  │
│  │ ▁▂▃▅▇█▇▅▃▁               │  │ locked IPs       1    │  │
│  │ enqueue rate  23.0 req/s  │  │ watched IPs      3    │  │
│  └───────────────────────────┘  └──────────────────────┘  │
│  [q] quit  [r] force refresh    updated: 12:34:56.789     │
╰────────────────────────────────────────────────────────────╯
```

### Panel guide

**QUEUE**
- `depth` — current pending requests (coloured: green < 50%, yellow < 80%, red ≥ 80% of capacity)
- `enqueued` / `dequeued` / `expired` / `dropped` — cumulative counters since server start
- `expired` and `dropped` are highlighted in yellow when non-zero

**THROUGHPUT**
- Sparkline of queue depth over the last 30 seconds (`▁` = low, `█` = peak)
- `enqueue rate` — requests enqueued per second (delta between polls)

**STORE**
- `results` — completed results waiting to be collected by long-poll clients
- `waiters` — currently open long-poll connections (highlighted in green when non-zero)

**SECURITY**
- `rate limiting` — `on` (green) or `off` (dim)
- `tracked IPs` — IPs currently with a token-bucket entry
- `throttled IPs` — IPs that have used all their tokens (yellow when > 0)
- `locked IPs` — IPs blocked due to repeated auth failures (red when > 0)
- `watched IPs` — IPs with ≥1 recorded auth failure (yellow when > 0)

### Key bindings

| Key | Action |
|-----|--------|
| `q` / `ctrl+c` | Quit the dashboard (server keeps running) |
| `r` | Force an immediate refresh |

---

## Stats Endpoint

`GET /debug/stats` returns the same data as the TUI in JSON form. Useful for scripted monitoring, alerting, or integration with custom dashboards.

```bash
curl -s http://localhost:8080/debug/stats | jq .
```

Full response schema documented in [API Reference](api-reference.md#get-debugstats).

### Alerting examples

```bash
# Alert if queue depth > 5000
depth=$(curl -s http://hub:8080/debug/stats | jq .queue.depth)
[ "$depth" -gt 5000 ] && alert "queue depth critical: $depth"

# Alert if any IPs are locked out
locked=$(curl -s http://hub:8080/debug/stats | jq .security.locked_ips)
[ "$locked" -gt 0 ] && alert "IPs locked out: $locked"
```

---

## Health Check

`GET /health` provides a lightweight signal for load balancers and orchestrators. No authentication required.

```bash
curl http://localhost:8080/health
# {"status":"ok","queue_depth":5,"uptime_seconds":3600}
```

---

## Prometheus Metrics

`GET /metrics` returns standard Go runtime and process metrics in Prometheus text format. Requires `X-Admin-API-Key` if `HR_ADMIN_API_KEY` is set.

```bash
curl -H "X-Admin-API-Key: secret" http://localhost:8080/metrics
```

Scrape configuration for Prometheus:

```yaml
scrape_configs:
  - job_name: hub-router
    static_configs:
      - targets: ["hub-router:8080"]
    metrics_path: /metrics
```

If `HR_ADMIN_API_KEY` is set, add:

```yaml
    authorization:
      type: Bearer
      credentials: <your-admin-key>
```
