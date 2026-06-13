package admission

import "testing"

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
