// Disk/SMART collector — polls local disk SMART data and pushes to VictoriaMetrics.
// Discovers disks from /sys/block, runs smartctl, parses JSON, pushes Prometheus metrics.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	vmPushURL       string
	collectInterval int
	host            string
)

// ATA SMART attribute IDs we care about (id -> metric suffix)
var ataAttrIDs = map[int]string{
	5:   "reallocated_sectors",
	9:   "power_on_hours",
	187: "reported_uncorrect",
	188: "command_timeout",
	197: "pending_sectors",
	198: "offline_uncorrectable",
}

func init() {
	if u := os.Getenv("VM_PUSH_URL"); u != "" {
		vmPushURL = u
	} else {
		vmPushURL = "http://victoriametrics:8428/api/v1/import/prometheus"
	}
	if s := os.Getenv("COLLECT_INTERVAL"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			collectInterval = n
		} else {
			collectInterval = 300
		}
	} else {
		collectInterval = 300
	}
	if h := os.Getenv("DISK_HOST"); h != "" {
		host = h
	} else {
		host = "localhost"
	}
}

func discoverDisks() []string {
	const sysBlock = "/sys/block"
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		log.Printf("warning: %s not found: %v", sysBlock, err)
		return nil
	}
	var disks []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "sd") && !strings.HasPrefix(name, "vd") &&
			!strings.HasPrefix(name, "xvd") && !strings.HasPrefix(name, "nvme") {
			continue
		}
		if strings.HasPrefix(name, "nvme") {
			parts := strings.Split(name, "n")
			if len(parts) > 1 && strings.Contains(parts[len(parts)-1], "p") {
				continue
			}
		}
		path := "/dev/" + name
		if _, err := os.Stat(path); err == nil {
			disks = append(disks, path)
		}
	}
	sort.Strings(disks)
	return disks
}

func getSmartctlJSON(ctx context.Context, device string) (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "smartctl", "-a", "-j", device)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func escapeLabel(val string) string {
	val = strings.ReplaceAll(val, "\\", "\\\\")
	val = strings.ReplaceAll(val, "\"", "\\\"")
	if len(val) > 255 {
		return val[:255]
	}
	return val
}

func getDiskSizeFromSys(device string) int64 {
	if !strings.HasPrefix(device, "/dev/") {
		return 0
	}
	name := strings.TrimPrefix(device, "/dev/")
	path := "/sys/block/" + name + "/size"
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || sectors <= 0 {
		return 0
	}
	return sectors * 512
}

func intFromAny(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(strings.Fields(x)[0])
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func parseTemp(val interface{}) *int {
	if val == nil {
		return nil
	}
	n, ok := intFromAny(val)
	if !ok || n < 0 || n > 100 {
		return nil
	}
	return &n
}

func getString(obj map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := obj[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

type metric struct {
	name   string
	value  int64
	labels map[string]string
}

func extractMetrics(device string, data map[string]interface{}) []metric {
	model := getString(data, "model_name", "serial_number")
	if model == "" {
		model = "unknown"
	}
	if len(model) > 64 {
		model = model[:64]
	}
	labels := map[string]string{"disk": device, "host": host, "model": escapeLabel(model)}

	var ms []metric

	// Capacity
	var capacity int64
	if uc, ok := data["user_capacity"].(map[string]interface{}); ok {
		if b, ok := uc["bytes"].(float64); ok {
			capacity = int64(b)
		}
	}
	if capacity <= 0 {
		if nsList, ok := data["nvme_namespaces"].([]interface{}); ok && len(nsList) > 0 {
			for _, ns := range nsList {
				n, ok := ns.(map[string]interface{})
				if !ok {
					continue
				}
				var nsze, lbas int64 = 0, 512
				if v, ok := n["nsze"]; ok {
					nsze, _ = int64FromAny(v)
				} else if v, ok := n["ncapacity"]; ok {
					nsze, _ = int64FromAny(v)
				}
				if lbaf, ok := n["lbaf"].([]interface{}); ok && len(lbaf) > 0 {
					if lb, ok := lbaf[0].(map[string]interface{}); ok {
						if ls, ok := lb["lba_size"]; ok {
							lbas, _ = int64FromAny(ls)
						}
					}
				}
				if nsze > 0 {
					capacity = nsze * lbas
					break
				}
			}
		}
	}
	if capacity <= 0 {
		blocks, _ := int64FromAny(data["scsi_user_data_blocks"])
		blen, _ := int64FromAny(data["scsi_logical_block_length"])
		if blen == 0 {
			blen = 512
		}
		if blocks > 0 && blen > 0 {
			capacity = blocks * blen
		}
	}
	if capacity <= 0 {
		capacity = getDiskSizeFromSys(device)
	}
	if capacity > 0 {
		ms = append(ms, metric{"disk_smart_capacity_bytes", capacity, copyLabels(labels)})
	}

	// Health status
	health := 0
	for _, key := range []string{"smart_status", "smartctl", "scsi_health_status"} {
		st := data[key]
		if m, ok := st.(map[string]interface{}); ok {
			if passed, ok := m["passed"].(bool); ok && passed {
				health = 1
				break
			}
			if passed, ok := m["passed"].(bool); ok && !passed {
				health = 0
				break
			}
		} else if b, ok := st.(bool); ok && b {
			health = 1
			break
		}
	}
	ms = append(ms, metric{"disk_smart_health_status", int64(health), copyLabels(labels)})

	// Temperature
	var tempVal *int
	if t, ok := data["temperature"]; ok {
		if m, ok := t.(map[string]interface{}); ok {
			tempVal = parseTemp(m["current"])
		} else {
			tempVal = parseTemp(t)
		}
	}
	if tempVal == nil {
		if nvme, ok := data["nvme_smart_health_information_log"].(map[string]interface{}); ok {
			tempVal = parseTemp(nvme["temperature"])
		}
	}
	if tempVal == nil {
		if ata, ok := data["ata_smart_attributes"].(map[string]interface{}); ok {
			if tbl, ok := ata["table"].([]interface{}); ok {
				for _, a := range tbl {
					attr, ok := a.(map[string]interface{})
					if !ok {
						continue
					}
					aid, _ := intFromAny(attr["id"])
					nameAttr := getString(attr, "name")
					if aid == 194 || strings.Contains(nameAttr, "Temperature") || strings.Contains(strings.ToLower(nameAttr), "temperature") {
						if raw, ok := attr["raw"].(map[string]interface{}); ok {
							sv := getString(raw, "string_value", "string")
							if sv != "" {
								tempVal = parseTemp(strings.Fields(sv)[0])
								break
							}
						}
					}
				}
			}
		}
	}
	if tempVal != nil {
		ms = append(ms, metric{"disk_smart_temperature_celsius", int64(*tempVal), copyLabels(labels)})
	}

	// ATA SMART attributes
	if ata, ok := data["ata_smart_attributes"].(map[string]interface{}); ok {
		if tbl, ok := ata["table"].([]interface{}); ok {
			for _, a := range tbl {
				attr, ok := a.(map[string]interface{})
				if !ok {
					continue
				}
				aid, ok := intFromAny(attr["id"])
				if !ok {
					continue
				}
				suffix, ok := ataAttrIDs[aid]
				if !ok {
					continue
				}
				var val int64
				if raw, ok := attr["raw"].(map[string]interface{}); ok {
					sv := getString(raw, "string_value", "string")
					if sv != "" {
						v, _ := int64FromAny(strings.Fields(sv)[0])
						val = v
					} else if v, ok := raw["value"]; ok {
						val, _ = int64FromAny(v)
					}
				} else {
					val, _ = int64FromAny(attr["raw"])
				}
				if suffix == "power_on_hours" && (val < 0 || val >= 1e7) {
					continue
				}
				ms = append(ms, metric{"disk_smart_" + suffix, val, copyLabels(labels)})
			}
		}
	}

	// NVMe
	if nvme, ok := data["nvme_smart_health_information_log"].(map[string]interface{}); ok {
		if poh, ok := nvme["power_on_hours"]; ok {
			if v, ok := int64FromAny(poh); ok && v >= 0 && v < 1e7 {
				ms = append(ms, metric{"disk_smart_power_on_hours", v, copyLabels(labels)})
			}
		}
		if pct, ok := nvme["percentage_used"]; ok {
			if v, ok := int64FromAny(pct); ok {
				ms = append(ms, metric{"disk_smart_percentage_used", v, copyLabels(labels)})
			}
		}
	}

	// SCSI
	if scsi, ok := data["scsi_grown_defect_list"]; ok {
		if v, ok := int64FromAny(scsi); ok {
			ms = append(ms, metric{"disk_smart_grown_defects", v, copyLabels(labels)})
		}
	}

	return ms
}

func int64FromAny(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case string:
		s := strings.Fields(x)
		if len(s) == 0 {
			return 0, false
		}
		n, err := strconv.ParseInt(s[0], 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func copyLabels(m map[string]string) map[string]string {
	n := make(map[string]string, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func formatPrometheus(metrics []metric) string {
	var buf strings.Builder
	for _, m := range metrics {
		var keys []string
		for k := range m.labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf(`%s="%s"`, k, escapeLabel(m.labels[k])))
		}
		buf.WriteString(m.name)
		buf.WriteString("{")
		buf.WriteString(strings.Join(parts, ","))
		buf.WriteString("} ")
		buf.WriteString(strconv.FormatInt(m.value, 10))
		buf.WriteString("\n")
	}
	return buf.String()
}

func pushMetrics(ctx context.Context, body string) bool {
	req, err := http.NewRequestWithContext(ctx, "POST", vmPushURL, bytes.NewBufferString(body))
	if err != nil {
		log.Printf("push failed: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "text/plain")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("push failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		log.Printf("push returned %d", resp.StatusCode)
		return false
	}
	return true
}

func collectAndPush(ctx context.Context) {
	disks := discoverDisks()
	if len(disks) == 0 {
		log.Printf("warning: no disks discovered")
		return
	}

	var allMetrics []metric
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, device := range disks {
		select {
		case <-ctx.Done():
			return
		default:
		}
		device := device
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := getSmartctlJSON(ctx, device)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("smartctl %s: %v", device, err)
				return
			}
			ms := extractMetrics(device, data)
			mu.Lock()
			allMetrics = append(allMetrics, ms...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(allMetrics) == 0 {
		log.Printf("warning: no metrics collected from any disk")
		return
	}

	body := formatPrometheus(allMetrics)
	if pushMetrics(ctx, body) {
		log.Printf("pushed %d metrics for %d disk(s)", len(allMetrics), len(disks))
	} else {
		log.Printf("warning: push returned non-204")
	}
}

func interruptibleSleep(ctx context.Context, d time.Duration) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// continue
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	log.Printf("starting — collect every %ds, host=%s", collectInterval, host)
	interruptibleSleep(ctx, 2*time.Second) // Brief delay for VictoriaMetrics

	for ctx.Err() == nil {
		collectAndPush(ctx)
		interruptibleSleep(ctx, time.Duration(collectInterval)*time.Second)
	}

	log.Printf("shutting down")
}
