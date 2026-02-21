# Local Monitoring System — Requirements & Solutions

## Requirements

### Core
- **Data ingestion**: HTTP push API to receive metrics from local services, PCs, and custom sources
- **Time-series storage**:
  - Raw data retained for 3 days (high resolution)
  - Aggregated data retained long-term (configurable rollup: 1min → 1h → 1d)
- **Dashboard UI**: visualize metrics — line charts, gauges, stat cards, tables, heatmaps
- **Resource-efficient**: runs on a mini PC (low RAM/CPU footprint, minimal deps)

### Data & API
- REST and/or InfluxDB line protocol push endpoints
- Support tags/labels on metrics for filtering and grouping
- Bulk push support (batch writes)
- Query API for dashboards and external consumers

### Dashboards
- Multiple dashboards per use case (system health, service status, custom KPIs)
- Variable/template support (filter by host, service, env)
- Time range selector with relative presets (last 1h, 6h, 1d, 3d)
- Panel types: time series, single stat, bar chart, table, gauge

### Alerting
- Threshold-based alerts on metrics
- Alert history log
- Notification channels: email, webhook (Slack/Telegram/etc.)

### Operations
- Service health/availability tracking (up/down status, uptime %)
- Configurable data retention and rollup policies
- Basic auth or token-based API security
- Lightweight agent for host metrics (CPU, RAM, disk, network)

---

## Candidate Solutions

### Option 1 — VictoriaMetrics + Grafana *(Recommended)*
| Aspect | Detail |
|--------|--------|
| Storage | VictoriaMetrics single-node binary (~20 MB, excellent compression, PromQL + MetricsQL) |
| Ingestion | Prometheus remote_write, InfluxDB line protocol, JSON push |
| Dashboards | Grafana OSS (rich panel library, alerting, variables) |
| Agent | `vmagent` or `node_exporter` + Pushgateway |
| RAM | ~100–200 MB total stack |
| Pros | Battle-tested, very low resource use, long-term storage built-in, Grafana ecosystem |
| Cons | Two separate services to operate; Grafana UI is large but functional |

### Option 2 — InfluxDB 2.x + Grafana
| Aspect | Detail |
|--------|--------|
| Storage | InfluxDB 2 (built-in Flux query, HTTP push, line protocol) |
| Dashboards | Grafana or InfluxDB's built-in UI |
| Agent | Telegraf (rich plugin ecosystem) |
| RAM | ~300–500 MB (InfluxDB 2 is heavier than VM) |
| Pros | Native push API + rich Telegraf input plugins; built-in UI fallback |
| Cons | Higher memory than VictoriaMetrics; Flux learning curve |

### Option 3 — Prometheus + Pushgateway + Grafana
| Aspect | Detail |
|--------|--------|
| Storage | Prometheus (pull-based; Pushgateway adapts to push) |
| Dashboards | Grafana |
| Agent | `node_exporter`, custom exporters |
| RAM | ~200–300 MB |
| Pros | Massive ecosystem, alerting via Alertmanager |
| Cons | Pushgateway is not designed for per-instance metrics; long-term storage needs Thanos/Cortex |

### Option 4 — Netdata
| Aspect | Detail |
|--------|--------|
| Storage | Built-in DBMS with automatic tiering (high-res short-term, aggregated long-term) |
| Dashboards | Built-in real-time UI (1-second resolution) |
| Agent | Bundled (auto-discovers services, processes, containers) |
| RAM | ~50–150 MB |
| Pros | Single install, zero config for host metrics, very fast |
| Cons | Custom push API limited; dashboard customization less flexible than Grafana |

### Option 5 — Custom Build (Go/Node + SQLite/TimescaleDB + React)
| Aspect | Detail |
|--------|--------|
| Storage | SQLite (simplest) or TimescaleDB (Postgres + hypertables + auto-rollup) |
| Dashboards | React + Chart.js / Recharts |
| Agent | Any HTTP client |
| RAM | 50–100 MB |
| Pros | Full control, zero external dependencies, tailor-made retention logic |
| Cons | Significant dev effort; missing alerting, auth, and dashboards out-of-the-box |

---

## Recommendation Matrix

| Requirement | VM+Grafana | InfluxDB+Grafana | Prometheus | Netdata | Custom |
|---|:---:|:---:|:---:|:---:|:---:|
| Push API | ✅ | ✅ | ⚠️ | ⚠️ | ✅ |
| Raw 3d + aggregated LT | ✅ | ✅ | ⚠️ | ✅ | ✅ |
| Rich dashboards | ✅ | ✅ | ✅ | ⚠️ | ⚠️ |
| Alerting | ✅ | ✅ | ✅ | ✅ | ❌ |
| Low resource use | ✅ | ⚠️ | ⚠️ | ✅ | ✅ |
| Minimal ops burden | ✅ | ⚠️ | ❌ | ✅ | ❌ |
| Custom data sources | ✅ | ✅ | ⚠️ | ⚠️ | ✅ |

**Primary recommendation**: **VictoriaMetrics + Grafana** — best balance of low resource use, native push API, long-term storage, and full-featured dashboards.
**Alternative if fully custom control needed**: Option 5 with TimescaleDB (handles rollups natively via continuous aggregates).
