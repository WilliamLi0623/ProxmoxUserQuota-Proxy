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

// usageToDelta converts a one-guest footprint into a Delta.
func usageToDelta(u usage.Usage) Delta {
	d := newDelta()
	d.Cores = u.Cores
	d.MemoryMiB = u.MemoryMiB
	d.Instances = u.Instances
	for st, b := range u.DiskBytes {
		d.DiskBytes[st] = b
	}
	return d
}

// CloneDelta is the footprint of a clone: the source's resources, +1 instance.
// A full clone with a target `storage` lands all disk on that storage.
func CloneDelta(src usage.Usage, params map[string]string) Delta {
	d := usageToDelta(src)
	d.Instances = 1
	if st := params["storage"]; st != "" {
		var total int64
		for _, b := range d.DiskBytes {
			total += b
		}
		d.DiskBytes = map[string]int64{st: total}
	}
	return d
}

// IncreaseDelta is the positive difference target-current per dimension, used
// for snapshot rollback (the older config may be larger).
func IncreaseDelta(target, current usage.Usage) Delta {
	d := newDelta()
	if target.Cores > current.Cores {
		d.Cores = target.Cores - current.Cores
	}
	if target.MemoryMiB > current.MemoryMiB {
		d.MemoryMiB = target.MemoryMiB - current.MemoryMiB
	}
	for st, tb := range target.DiskBytes {
		if tb > current.DiskBytes[st] {
			d.DiskBytes[st] = tb - current.DiskBytes[st]
		}
	}
	return d
}

// MoveDelta is the disk a move_disk/move_volume lands on the target storage.
// Moving within the same storage is a no-op for quota.
func MoveDelta(kind string, params, cur map[string]string) Delta {
	d := newDelta()
	key := params["disk"]
	if key == "" {
		key = params["volume"] // lxc move_volume
	}
	target := params["storage"]
	if key == "" || target == "" {
		return d
	}
	st, size, hasSize, _, _ := usage.ParseDisk(cur[key])
	if st == target {
		return d
	}
	if hasSize {
		d.DiskBytes[target] = size
	}
	return d
}

// StorageAllocDelta is the size a raw volume allocation adds on a storage.
func StorageAllocDelta(storage string, params map[string]string) Delta {
	d := newDelta()
	if storage == "" {
		return d
	}
	if n, ok := usage.ParseSize(params["size"]); ok && n > 0 {
		d.DiskBytes[storage] = n
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
