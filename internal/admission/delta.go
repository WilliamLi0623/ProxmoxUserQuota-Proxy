package admission

import (
	"strconv"
	"strings"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/usage"
)

// Delta is the resource increase a request would cause. Only positive values
// matter for admission; shrinking or non-resource edits yield zero/negative.
type Delta struct {
	Cores     int
	MemoryMiB int64
	Instances int
	DiskBytes map[string]int64
}

func newDelta() Delta { return Delta{DiskBytes: map[string]int64{}} }

func (d Delta) positive() bool {
	if d.Cores > 0 || d.MemoryMiB > 0 || d.Instances > 0 {
		return true
	}
	for _, b := range d.DiskBytes {
		if b > 0 {
			return true
		}
	}
	return false
}

// CreateDelta is the footprint of a brand-new guest.
func CreateDelta(kind string, p map[string]string) Delta {
	d := newDelta()
	d.Instances = 1
	if kind == "qemu" {
		d.Cores = atoiDef(p["sockets"], 1) * atoiDef(p["cores"], 1)
		d.MemoryMiB = int64(atoiDef(p["memory"], 512))
	} else {
		d.Cores = atoiDef(p["cores"], 0)
		d.MemoryMiB = int64(atoiDef(p["memory"], 512))
	}
	addDiskParams(d.DiskBytes, p, nil)
	return d
}

// ConfigDelta is the increase from a config edit, measured against the current
// config. Pure non-resource edits and shrinking edits produce no positive delta.
func ConfigDelta(kind string, p, cur map[string]string) Delta {
	d := newDelta()
	if hasAny(p, "sockets", "cores") {
		d.Cores = totalCores(kind, merge(cur, p)) - totalCores(kind, cur)
	}
	if _, ok := p["memory"]; ok {
		d.MemoryMiB = int64(atoiDef(p["memory"], 512)) - int64(atoiDef(cur["memory"], 512))
	}
	// Disks newly added via the config edit (key absent in the current config).
	addDiskParams(d.DiskBytes, p, cur)
	return d
}

// ResizeDelta is the disk growth from a PUT .../resize.
func ResizeDelta(p, cur map[string]string) Delta {
	d := newDelta()
	disk, sz := p["disk"], p["size"]
	if disk == "" || sz == "" {
		return d
	}
	st, curSize, hasSize, _, _ := usage.ParseDisk(cur[disk])
	if st == "" {
		return d
	}
	if rest, ok := strings.CutPrefix(sz, "+"); ok {
		if n, ok := usage.ParseSize(rest); ok {
			d.DiskBytes[st] = n
		}
		return d
	}
	if n, ok := usage.ParseSize(sz); ok && hasSize && n > curSize {
		d.DiskBytes[st] = n - curSize
	}
	return d
}

// addDiskParams adds the sizes of create-style disk params to dst. When cur is
// non-nil, only keys absent from cur count (genuinely new disks).
func addDiskParams(dst map[string]int64, p, cur map[string]string) {
	for k, v := range p {
		if !usage.IsDiskKey(k) {
			continue
		}
		if cur != nil {
			if _, exists := cur[k]; exists {
				continue
			}
		}
		st, b, cdrom := parseCreateDisk(v)
		if cdrom || st == "" {
			continue
		}
		dst[st] += b
	}
}

// parseCreateDisk parses a create-time disk spec: "pool:32" (32 GiB on pool),
// "pool:32,ssd=1", "local:iso/x.iso,media=cdrom", or explicit "...,size=32G".
func parseCreateDisk(v string) (storage string, bytes int64, cdrom bool) {
	fields := strings.Split(v, ",")
	first := strings.TrimSpace(fields[0])
	if first == "" || first == "none" {
		return
	}
	st, rest, ok := strings.Cut(first, ":")
	if !ok || st == "" {
		return
	}
	storage = st
	for _, f := range fields[1:] {
		f = strings.TrimSpace(f)
		if f == "media=cdrom" {
			cdrom = true
		}
		if s, found := strings.CutPrefix(f, "size="); found {
			if n, ok := usage.ParseSize(s); ok {
				bytes = n
			}
		}
	}
	if bytes == 0 && !cdrom {
		if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && n > 0 {
			bytes = int64(n) << 30 // create-time "storage:NN" => NN GiB
		}
	}
	return
}

func totalCores(kind string, cfg map[string]string) int {
	if kind == "qemu" {
		return atoiDef(cfg["sockets"], 1) * atoiDef(cfg["cores"], 1)
	}
	return atoiDef(cfg["cores"], 0)
}

func merge(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func hasAny(m map[string]string, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func atoiDef(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}
