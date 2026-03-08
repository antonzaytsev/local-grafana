#!/usr/bin/env python3
"""
Disk/SMART collector — polls local disk SMART data and pushes to VictoriaMetrics.
Discovers disks from /sys/block, runs smartctl, parses JSON, pushes Prometheus metrics.
"""

import json
import logging
import os
import subprocess
import sys
import time
from urllib.request import Request, urlopen
from urllib.error import URLError, HTTPError

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(name)s %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stdout)],
)
LOG = logging.getLogger("disk-collector")

VM_PUSH_URL = os.environ.get("VM_PUSH_URL", "http://victoriametrics:8428/api/v1/import/prometheus")
COLLECT_INTERVAL = int(os.environ.get("COLLECT_INTERVAL", "300"))  # 5 min default
HOST = os.environ.get("DISK_HOST", "localhost")

# ATA SMART attribute IDs we care about (id -> metric suffix)
ATA_ATTR_IDS = {
    5: "reallocated_sectors",
    9: "power_on_hours",
    187: "reported_uncorrect",
    188: "command_timeout",
    197: "pending_sectors",
    198: "offline_uncorrectable",
    194: "temperature_celsius",
    190: "airflow_temperature",
}


def discover_disks():
    """Find block devices from /sys/block. Returns list of /dev/X paths."""
    disks = []
    sys_block = "/sys/block"
    if not os.path.exists(sys_block):
        LOG.warning("%s not found", sys_block)
        return disks

    for name in os.listdir(sys_block):
        if name.startswith(("sd", "vd", "xvd", "nvme")):
            # Skip partition-like names (nvme0n1 is disk, nvme0n1p1 is partition)
            if "nvme" in name and "p" in name.split("n")[-1]:
                continue
            path = f"/dev/{name}"
            if os.path.exists(path):
                disks.append(path)
    return sorted(disks)


def get_smartctl_json(device):
    """Run smartctl -a -j on device. Returns parsed JSON or None."""
    try:
        out = subprocess.run(
            ["smartctl", "-a", "-j", device],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if out.returncode != 0:
            LOG.debug("smartctl %s returned %d: %s", device, out.returncode, out.stderr[:200])
            return None
        return json.loads(out.stdout)
    except subprocess.TimeoutExpired:
        LOG.warning("smartctl %s timed out", device)
        return None
    except (json.JSONDecodeError, FileNotFoundError) as e:
        LOG.debug("smartctl %s failed: %s", device, e)
        return None


def escape_label(val):
    """Escape Prometheus label value: backslash and double-quote."""
    if val is None:
        return ""
    s = str(val).replace("\\", "\\\\").replace('"', '\\"')
    return s[:255]  # reasonable limit


def extract_metrics(device, data):
    """Extract disk_smart_* metrics from smartctl JSON. Yields (name, value, labels)."""
    labels = {"disk": device, "host": HOST}
    model = data.get("model_name") or data.get("serial_number") or "unknown"
    labels["model"] = escape_label(model[:64])

    # Health status: 1=passed, 0=failed
    health = 0
    for key in ("smart_status", "smartctl", "scsi_health_status"):
        st = data.get(key)
        if isinstance(st, dict):
            passed = st.get("passed")
            if passed is True:
                health = 1
                break
            if passed is False:
                health = 0
                break
        elif isinstance(st, bool) and st:
            health = 1
            break
    yield ("disk_smart_health_status", health, labels)

    # Temperature — multiple possible locations
    temp = None
    if "temperature" in data and isinstance(data["temperature"], dict):
        temp = data["temperature"].get("current")
    if temp is None and "temperature" in data:
        temp = data["temperature"]
    if temp is None:
        nvme = data.get("nvme_smart_health_information_log", {})
        if isinstance(nvme, dict):
            temp = nvme.get("temperature")
    if temp is None:
        ata = data.get("ata_smart_attributes", {})
        if isinstance(ata, dict):
            tbl = ata.get("table") or []
            for attr in tbl:
                if isinstance(attr, dict):
                    aid = attr.get("id")
                    name_attr = attr.get("name", "")
                    if aid == 194 or "Temperature" in name_attr or "temperature" in name_attr.lower():
                        raw = attr.get("raw", {})
                        if isinstance(raw, dict):
                            temp = raw.get("value") or raw.get("string_value")
                        elif isinstance(raw, (int, float)):
                            temp = raw
                        break
    if temp is not None:
        try:
            temp_val = int(float(temp))
            yield ("disk_smart_temperature_celsius", temp_val, labels)
        except (ValueError, TypeError):
            pass

    # ATA SMART attributes
    ata = data.get("ata_smart_attributes", {})
    tbl = ata.get("table") or []
    for attr in tbl:
        if not isinstance(attr, dict):
            continue
        aid = attr.get("id")
        if aid not in ATA_ATTR_IDS:
            continue
        raw = attr.get("raw", {})
        if isinstance(raw, dict):
            val = raw.get("value")
            if val is None:
                val = raw.get("string_value", "0")
        else:
            val = raw
        try:
            num = int(float(str(val).split()[0]))  # "42" or "42 (raw)" -> 42
            metric_name = f"disk_smart_{ATA_ATTR_IDS[aid]}"
            yield (metric_name, num, labels)
        except (ValueError, TypeError):
            pass

    # NVMe: power_on_hours, percentage_used
    nvme = data.get("nvme_smart_health_information_log", {})
    if isinstance(nvme, dict):
        poh = nvme.get("power_on_hours")
        if poh is not None:
            try:
                yield ("disk_smart_power_on_hours", int(poh), labels)
            except (ValueError, TypeError):
                pass
        pct = nvme.get("percentage_used")
        if pct is not None:
            try:
                yield ("disk_smart_percentage_used", int(pct), labels)
            except (ValueError, TypeError):
                pass

    # SCSI / SAS
    scsi = data.get("scsi_grown_defect_list")  # or other scsi_* keys
    if scsi is not None and isinstance(scsi, (int, float)):
        yield ("disk_smart_grown_defects", int(scsi), labels)


def format_prometheus(metrics):
    """Format list of (name, value, labels) as Prometheus text."""
    lines = []
    for name, value, labels in metrics:
        lbl = ",".join(f'{k}="{escape_label(v)}"' for k, v in sorted(labels.items()))
        lines.append(f"{name}{{{lbl}}} {value}")
    return "\n".join(lines)


def push_metrics(body):
    """POST metrics to VictoriaMetrics. Returns True on success."""
    try:
        req = Request(VM_PUSH_URL, data=body.encode(), method="POST")
        req.add_header("Content-Type", "text/plain")
        with urlopen(req, timeout=10) as r:
            return r.status == 204
    except (URLError, HTTPError) as e:
        LOG.warning("push failed: %s", e)
        return False


def collect_and_push():
    disks = discover_disks()
    if not disks:
        LOG.warning("no disks discovered")
        return

    all_metrics = []
    for device in disks:
        data = get_smartctl_json(device)
        if data:
            for m in extract_metrics(device, data):
                all_metrics.append(m)

    if not all_metrics:
        LOG.warning("no metrics collected from any disk")
        return

    body = format_prometheus(all_metrics)
    if push_metrics(body):
        LOG.info("pushed %d metrics for %d disk(s)", len(all_metrics), len(disks))
    else:
        LOG.warning("push returned non-204")


def main():
    LOG.info("starting — collect every %ds, host=%s", COLLECT_INTERVAL, HOST)
    time.sleep(5)

    while True:
        try:
            collect_and_push()
        except Exception as e:
            LOG.exception("collect error: %s", e)
        time.sleep(COLLECT_INTERVAL)


if __name__ == "__main__":
    main()
