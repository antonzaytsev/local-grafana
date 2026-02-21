# Local Monitoring Stack

VictoriaMetrics + Grafana + vmagent + node-exporter, deployed via Docker Compose.

## Services

| Service | Description |
|---------|-------------|
| **grafana** | Dashboard UI — visualizes all metrics, home page shows Keenetic router |
| **victoriametrics** | Time-series database — stores all metrics, exposes PromQL query API |
| **vmagent** | Scrapes node-exporter, receives push metrics, forwards to VictoriaMetrics with 1h rollups |
| **node-exporter** | Exposes host machine metrics (CPU, RAM, disk, network) for vmagent to scrape |
| **keenetic-collector** | Ruby service — polls Keenetic router every 60s and pushes CPU/memory/uptime to VictoriaMetrics |
| **docs** | Serves push API reference as plain text over HTTP |

All ports are configurable via `.env` (see `.env.example`).

## Quick Start

```bash
cp .env.example .env
# edit .env if needed
docker compose up -d
```

Open Grafana at http://localhost:8430 — login: `admin` / `admin`.

## Pushing Custom Metrics

See full reference at **http://localhost:8431/push-api.md**

Quick example (Prometheus text format):

```bash
curl -X POST http://localhost:8429/api/v1/import/prometheus \
  -H 'Content-Type: text/plain' \
  --data-raw 'my_metric{host="server1"} 42'
```

Three formats supported: Prometheus text, JSON line, InfluxDB line protocol.

## Data Retention

### Architecture

```
vmagent
  ├── raw metrics → VictoriaMetrics (as-is, 15s resolution)
  └── 1h aggregates → VictoriaMetrics (new metric names: metric:1h_avg, :1h_max, :1h_min, :1h_last)
```

Both raw and aggregated metrics live in the same VictoriaMetrics instance.

### Retention Period

Controlled by `VM_RETENTION` in `.env` (default: `90d`).

| Setting | Use case |
|---------|----------|
| `30d` | Tight on disk |
| `90d` | Default — good balance for mini PC |
| `365d` | 1 year, ~1–5 GB for typical homelab metrics |

VictoriaMetrics compresses data ~10x vs raw storage. Disk use for a typical setup (node-exporter + 10 custom metrics at 15s interval):
- 30d ≈ 50–150 MB
- 365d ≈ 500 MB – 2 GB

### Querying Aggregated Data

For long-range dashboards, use the `*:1h_avg` metrics instead of raw:

```promql
# Raw (good for last few days)
node_cpu_seconds_total{mode="idle"}

# Aggregated (efficient for months/years)
node_cpu_seconds_total:1h_avg{mode="idle"}
```

### Changing Retention

Edit `VM_RETENTION` in `.env`, then restart:

```bash
docker compose restart victoriametrics
```

> **Note:** Reducing retention does not immediately delete old data. VictoriaMetrics removes expired data during background maintenance (within 24h).

## Dashboards

Three pre-provisioned dashboards in Grafana:

- **Host Overview** — CPU, RAM, disk, network time series and stats
- **Services Status** — up/down per scrape target, availability %, table
- **Custom Metrics** — generic explorer: filter by host and metric name

## Configuration Files

```
docker-compose.yml          # service definitions
.env.example                # port and retention config template
vmagent/
  scrape.yml                # which targets vmagent scrapes
  stream-aggregation.yml    # 1h rollup rules
grafana/
  provisioning/
    datasources/vm.yml      # auto-configured VictoriaMetrics datasource
    dashboards/             # dashboard JSON + provider config
docs/
  push-api.md               # push API reference (served on :8431)
```
