// Package usage computes a user's allocation-based resource consumption by
// summing the configured values of every guest in their pool, following the
// quota model in ProxmoxUserQuota-Docs/quota-model.md.
//
// Accounting is config-based, not runtime-based: a stopped guest still counts.
// Disk is summed per named storage and includes unused[n] volumes.
package usage

import (
	"strconv"
	"strings"
)

// Usage is a user's total consumption across their pool.
type Usage struct {
	Cores     int              `json:"cores"`
	MemoryMiB int64            `json:"memory_mib"`
	Instances int              `json:"instances"`
	DiskBytes map[string]int64 `json:"disk_bytes"` // per storage id
}

// DiskGiB returns per-storage disk usage rounded up to whole GiB (the unit the
// quota store uses). Rounding up is the conservative choice for enforcement.
func (u Usage) DiskGiB() map[string]int64 {
	out := make(map[string]int64, len(u.DiskBytes))
	for st, b := range u.DiskBytes {
		out[st] = (b + (1 << 30) - 1) / (1 << 30)
	}
	return out
}

// Resources is the contribution of a single guest. Unused holds disks whose
// size is not in the config line and must be resolved via the storage API.
type Resources struct {
	Cores     int
	MemoryMiB int64
	Disks     map[string]int64 // storage -> bytes (size known from config)
	Unused    []UnusedRef
}

// UnusedRef is an unused[n] volume needing a size lookup.
type UnusedRef struct {
	Storage string
	Volid   string
}

// GuestResources extracts cores, memory and disk allocation from a guest
// config map. kind is "qemu" or "lxc".
func GuestResources(kind string, cfg map[string]string) Resources {
	r := Resources{Disks: map[string]int64{}}

	if kind == "qemu" {
		sockets := atoiDefault(cfg["sockets"], 1)
		cores := atoiDefault(cfg["cores"], 1)
		r.Cores = sockets * cores
		r.MemoryMiB = int64(atoiDefault(cfg["memory"], 512))
	} else { // lxc
		r.Cores = atoiDefault(cfg["cores"], 0) // unset = unlimited; counted as 0
		r.MemoryMiB = int64(atoiDefault(cfg["memory"], 512))
	}

	for k, v := range cfg {
		if !isDiskKey(k) {
			continue
		}
		st, size, hasSize, cdrom, volid := ParseDisk(v)
		if cdrom || st == "" {
			continue
		}
		if hasSize {
			r.Disks[st] += size
		} else if volid != "" {
			r.Unused = append(r.Unused, UnusedRef{Storage: st, Volid: volid})
		}
	}
	return r
}

// ParseDisk parses a PVE disk config value, e.g.
// "local-lvm:vm-100-disk-0,size=32G,ssd=1" or "local:iso/x.iso,media=cdrom"
// or an unused entry "local-lvm:vm-100-disk-1" (no size).
func ParseDisk(v string) (storage string, size int64, hasSize, cdrom bool, volid string) {
	fields := strings.Split(v, ",")
	first := strings.TrimSpace(fields[0])
	if first == "" || first == "none" {
		return
	}
	st, _, ok := strings.Cut(first, ":")
	if !ok || st == "" {
		return
	}
	storage = st
	volid = first
	for _, f := range fields[1:] {
		f = strings.TrimSpace(f)
		if f == "media=cdrom" {
			cdrom = true
		}
		if s, found := strings.CutPrefix(f, "size="); found {
			if n, ok := parseSize(s); ok {
				size = n
				hasSize = true
			}
		}
	}
	return
}

var diskPrefixes = []string{"scsi", "virtio", "sata", "ide", "mp", "unused"}

func isDiskKey(k string) bool {
	if k == "rootfs" || k == "efidisk0" || k == "tpmstate0" {
		return true
	}
	for _, p := range diskPrefixes {
		if rest, ok := strings.CutPrefix(k, p); ok && allDigits(rest) {
			return true
		}
	}
	return false
}

// parseSize parses a PVE size such as "32G", "528K", "4M", "1T" or a plain
// byte count into bytes. Suffixes are binary (1024-based).
func parseSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult = 1 << 10
		s = s[:len(s)-1]
	case 'm', 'M':
		mult = 1 << 20
		s = s[:len(s)-1]
	case 'g', 'G':
		mult = 1 << 30
		s = s[:len(s)-1]
	case 't', 'T':
		mult = 1 << 40
		s = s[:len(s)-1]
	case 'p', 'P':
		mult = 1 << 50
		s = s[:len(s)-1]
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n * mult, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f * float64(mult)), true
	}
	return 0, false
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
