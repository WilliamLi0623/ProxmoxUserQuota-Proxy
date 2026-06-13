package admission

import (
	"testing"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/usage"
)

func TestCreateDeltaQEMU(t *testing.T) {
	d := CreateDelta("qemu", map[string]string{
		"sockets": "2", "cores": "4", "memory": "8192",
		"scsi0": "pool:32", "efidisk0": "pool:1",
		"ide2": "local:iso/x.iso,media=cdrom",
	})
	if d.Cores != 8 || d.MemoryMiB != 8192 || d.Instances != 1 {
		t.Fatalf("%+v", d)
	}
	if want := int64(33) << 30; d.DiskBytes["pool"] != want { // 32 + 1, cdrom excluded
		t.Errorf("disk=%d want %d", d.DiskBytes["pool"], want)
	}
}

func TestCreateDeltaLXC(t *testing.T) {
	d := CreateDelta("lxc", map[string]string{
		"cores": "2", "memory": "1024", "rootfs": "pool:8",
	})
	if d.Cores != 2 || d.MemoryMiB != 1024 || d.DiskBytes["pool"] != 8<<30 {
		t.Fatalf("%+v", d)
	}
}

func TestConfigDeltaIncrease(t *testing.T) {
	cur := map[string]string{"sockets": "1", "cores": "2", "memory": "2048"}
	d := ConfigDelta("qemu", map[string]string{"cores": "4", "memory": "4096"}, cur)
	if d.Cores != 2 {
		t.Errorf("cores delta=%d want 2", d.Cores)
	}
	if d.MemoryMiB != 2048 {
		t.Errorf("mem delta=%d want 2048", d.MemoryMiB)
	}
}

func TestConfigDeltaShrinkAndNonResource(t *testing.T) {
	cur := map[string]string{"cores": "4", "memory": "4096"}
	d := ConfigDelta("qemu", map[string]string{"memory": "2048", "description": "hi"}, cur)
	if d.positive() {
		t.Errorf("shrink/non-resource edit should not be positive: %+v", d)
	}
}

func TestConfigDeltaAddDisk(t *testing.T) {
	cur := map[string]string{"scsi0": "pool:vm-1-disk-0,size=10G"}
	d := ConfigDelta("qemu", map[string]string{"scsi1": "pool:50"}, cur)
	if d.DiskBytes["pool"] != 50<<30 {
		t.Errorf("disk delta=%d want 50 GiB", d.DiskBytes["pool"])
	}
}

func TestResizeDeltaRelative(t *testing.T) {
	cur := map[string]string{"scsi0": "pool:vm-1-disk-0,size=10G"}
	d := ResizeDelta(map[string]string{"disk": "scsi0", "size": "+5G"}, cur)
	if d.DiskBytes["pool"] != 5<<30 {
		t.Errorf("disk delta=%d want 5 GiB", d.DiskBytes["pool"])
	}
}

func TestResizeDeltaAbsolute(t *testing.T) {
	cur := map[string]string{"scsi0": "pool:vm-1-disk-0,size=10G"}
	d := ResizeDelta(map[string]string{"disk": "scsi0", "size": "25G"}, cur)
	if d.DiskBytes["pool"] != 15<<30 {
		t.Errorf("disk delta=%d want 15 GiB", d.DiskBytes["pool"])
	}
}

func TestCloneDelta(t *testing.T) {
	src := usage.Usage{Cores: 4, MemoryMiB: 8192, Instances: 1,
		DiskBytes: map[string]int64{"pool": 32 << 30}}
	d := CloneDelta(src, map[string]string{})
	if d.Cores != 4 || d.MemoryMiB != 8192 || d.Instances != 1 ||
		d.DiskBytes["pool"] != 32<<30 {
		t.Fatalf("clone delta %+v", d)
	}
	d2 := CloneDelta(src, map[string]string{"storage": "fast"})
	if d2.DiskBytes["fast"] != 32<<30 || d2.DiskBytes["pool"] != 0 {
		t.Errorf("clone-to-storage should move all disk to fast: %+v", d2.DiskBytes)
	}
}

func TestIncreaseDelta(t *testing.T) {
	target := usage.Usage{Cores: 8, MemoryMiB: 16384,
		DiskBytes: map[string]int64{"pool": 50 << 30}}
	current := usage.Usage{Cores: 4, MemoryMiB: 16384,
		DiskBytes: map[string]int64{"pool": 30 << 30}}
	d := IncreaseDelta(target, current)
	if d.Cores != 4 {
		t.Errorf("cores delta=%d want 4", d.Cores)
	}
	if d.MemoryMiB != 0 {
		t.Errorf("mem delta=%d want 0 (no increase)", d.MemoryMiB)
	}
	if d.DiskBytes["pool"] != 20<<30 {
		t.Errorf("disk delta=%d want 20 GiB", d.DiskBytes["pool"])
	}
}

func TestMoveDelta(t *testing.T) {
	cur := map[string]string{"scsi0": "pool:vm-1-disk-0,size=10G"}
	d := MoveDelta("qemu", map[string]string{"disk": "scsi0", "storage": "fast"}, cur)
	if d.DiskBytes["fast"] != 10<<30 {
		t.Errorf("move delta=%d want 10 GiB on fast", d.DiskBytes["fast"])
	}
	d2 := MoveDelta("qemu", map[string]string{"disk": "scsi0", "storage": "pool"}, cur)
	if d2.positive() {
		t.Errorf("same-storage move should be a no-op: %+v", d2)
	}
}

func TestStorageAllocDelta(t *testing.T) {
	d := StorageAllocDelta("pool", map[string]string{"size": "16G"})
	if d.DiskBytes["pool"] != 16<<30 {
		t.Errorf("alloc delta=%d want 16 GiB", d.DiskBytes["pool"])
	}
}
