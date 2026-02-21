# Implementation Plan: Local Monitoring Stack

## Context
Build a local metrics monitoring system for a mini PC environment. Needs push-based ingestion, 3-day raw retention with long-term aggregated storage, and rich dashboards. Stack: **VictoriaMetrics + Grafana** via Docker Compose.

---

## Architecture

```
[Services / PCs]
      │  HTTP push
      ▼
 vmagent (push receiver)
      │  remote_write
      ▼
VictoriaMetrics (storage + query)
      │  PromQL
      ▼
   Grafana (dashboards)
```

Host metrics collected by `node-exporter` → scraped by `vmagent`.

---

## Phases

### Phase 1 — Core Stack
- `docker-compose.yml` with:
  - **VictoriaMetrics** single-node (`victoriametrics/victoria-metrics`)
    - retention: 3 days raw, auto-downsampling for long-term
    - volumes: persistent data dir
  - **Grafana OSS** (`grafana/grafana`)
    - provisioned datasource pointing to VM
    - persistent volume for dashboards/config
  - **vmagent** (`victoriametrics/vmagent`)
    - scrape `node-exporter`
    - expose push endpoint (Prometheus remote_write + InfluxDB line protocol)
  - **node-exporter** (`prom/node-exporter`)
    - host CPU, RAM, disk, network metrics
- `.env` file for ports, retention settings, credentials

### Phase 2 — Push API & Custom Metrics
- Document push endpoints exposed by vmagent:
  - `POST /api/v1/import/prometheus` — Prometheus text format
  - `POST /api/v1/import` — JSON line format
  - `POST /write` — InfluxDB line protocol
- Example curl/code snippets for each format
- Basic auth on vmagent push endpoints via nginx basic auth
- Serve `docs/push-api.md` as static HTML via Grafana's built-in static file serving or a lightweight nginx container, accessible at `http://<host>/docs/push-api`

### Phase 3 — Dashboards
- Provision base dashboards as JSON in `grafana/dashboards/`:
  - **Host Overview**: CPU, RAM, disk, network (sourced from node-exporter)
  - **Services Status**: up/down per service, uptime %
  - **Custom Metrics**: generic template dashboard with variable selectors (host, service)
- Grafana variables for filtering by host/label

### Phase 4 — Data Retention Config
- VictoriaMetrics flags:
  - `-retentionPeriod=3d` for raw (or longer with downsampling)
  - Use VM's built-in downsampling (`-downsampling.period`) for long-term aggregates
- Document retention policy in README

---

## Files to Create

```
docker-compose.yml
.env.example
vmagent/
  scrape.yml           # scrape configs (node-exporter + self)
grafana/
  provisioning/
    datasources/vm.yml
    dashboards/dashboards.yml
  dashboards/
    host-overview.json
    services-status.json
    custom-metrics.json
docs/
  push-api.md          # push API reference (served via HTTP)
README.md              # setup instructions, links to docs
```

---

## Next Version

- **Alerting**: Grafana unified alerting (built-in, no Alertmanager needed)
  - Rules: CPU > 90% sustained, service down (metric absent), disk > 85%
  - Notification contact point: webhook (Telegram/Slack) configured via env var
- **Log aggregation**: Loki + Promtail for service/container logs alongside metrics

---

## Verification
1. `docker compose up -d` — all containers healthy
2. `curl http://localhost:8428/metrics` — VictoriaMetrics responds
3. Push a test metric via curl to vmagent push endpoint, verify it appears in VM
4. Open Grafana, confirm datasource query works, panels show data
5. Trigger a threshold breach, confirm alert fires
