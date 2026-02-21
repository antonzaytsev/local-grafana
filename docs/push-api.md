# Push API Reference

Base URL for all push endpoints: `http://<host>:8429`

vmagent accepts metrics in three formats. All endpoints return HTTP 204 on success.

---

## 1. Prometheus Text Format

**Endpoint:** `POST /api/v1/import/prometheus`

Each line: `metric_name{label="value"} numeric_value [unix_timestamp_ms]`
Timestamp is optional — vmagent uses current time if omitted.

```bash
curl -X POST http://localhost:8429/api/v1/import/prometheus \
  -H 'Content-Type: text/plain' \
  --data-raw '
my_service_requests_total{host="server1",env="prod"} 1500
my_service_errors_total{host="server1",env="prod"} 3
my_service_latency_ms{host="server1",env="prod"} 42.7
'
```

Multiple metrics in one request are supported. Send as many lines as needed per request.

---

## 2. JSON Line Format

**Endpoint:** `POST /api/v1/import`

Each line is a JSON object with `metric` (labels map) and `values`/`timestamps` arrays.

```bash
curl -X POST http://localhost:8429/api/v1/import \
  -H 'Content-Type: application/json' \
  --data-raw '
{"metric":{"__name__":"my_service_requests_total","host":"server1","env":"prod"},"values":[1500],"timestamps":[1700000000000]}
{"metric":{"__name__":"my_service_errors_total","host":"server1","env":"prod"},"values":[3],"timestamps":[1700000000000]}
'
```

`timestamps` are Unix milliseconds. Send multiple JSON objects, one per line (newline-delimited JSON).

---

## 3. InfluxDB Line Protocol

**Endpoint:** `POST /write`

Format: `measurement,tag1=val1,tag2=val2 field1=val1,field2=val2 [unix_timestamp_ns]`
Timestamp is nanoseconds and is optional.

```bash
curl -X POST http://localhost:8429/write \
  -H 'Content-Type: text/plain' \
  --data-raw '
my_service,host=server1,env=prod requests=1500,errors=3,latency_ms=42.7
disk_usage,host=server1,mount=/data used_bytes=10737418240,free_bytes=53687091200
'
```

---

## Common Patterns

### Push from shell script

```bash
#!/bin/bash
PUSH_URL="http://localhost:8429/api/v1/import/prometheus"
HOST=$(hostname)
TIMESTAMP=$(date +%s%3N)

CPU=$(top -bn1 | grep "Cpu(s)" | awk '{print $2}')

curl -s -X POST "$PUSH_URL" \
  -H 'Content-Type: text/plain' \
  --data-raw "host_cpu_usage{host=\"$HOST\"} $CPU"
```

### Push from Python

```python
import requests, time, socket

PUSH_URL = "http://localhost:8429/api/v1/import/prometheus"
HOST = socket.gethostname()

def push_metric(name, value, labels=None):
    label_str = ",".join(f'{k}="{v}"' for k, v in (labels or {}).items())
    line = f'{name}{{{label_str}}} {value}'
    requests.post(PUSH_URL, data=line, headers={"Content-Type": "text/plain"})

push_metric("my_service_requests_total", 1500, {"host": HOST, "env": "prod"})
push_metric("my_service_latency_ms", 42.7, {"host": HOST, "env": "prod"})
```

### Push from Node.js

```javascript
const PUSH_URL = "http://localhost:8429/api/v1/import/prometheus";
const HOST = require("os").hostname();

async function pushMetric(name, value, labels = {}) {
  const labelStr = Object.entries(labels).map(([k, v]) => `${k}="${v}"`).join(",");
  const line = `${name}{${labelStr}} ${value}`;
  await fetch(PUSH_URL, { method: "POST", body: line });
}

await pushMetric("my_service_requests_total", 1500, { host: HOST, env: "prod" });
```

---

## Querying Data

After pushing, query via VictoriaMetrics PromQL at `http://localhost:8428`:

```bash
# Instant query
curl "http://localhost:8428/api/v1/query?query=my_service_requests_total"

# Range query (last 1 hour)
START=$(date -d '1 hour ago' +%s 2>/dev/null || date -v-1H +%s)
curl "http://localhost:8428/api/v1/query_range?query=my_service_requests_total&start=$START&end=$(date +%s)&step=60"
```

---

## Metric Naming Conventions

- Use `snake_case`
- Suffix with unit: `_bytes`, `_ms`, `_seconds`, `_total`
- Counter metrics (ever-increasing): suffix `_total`
- Gauge metrics (can go up/down): no special suffix
- Always include `host` label for per-machine metrics
