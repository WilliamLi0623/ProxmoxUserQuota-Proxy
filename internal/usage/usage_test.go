package usage

import "testing"

func TestParseDiskActive(t *testing.T) {
	st, size, hasSize, cdrom, volid := ParseDisk("local-lvm:vm-100-disk-0,size=32G,ssd=1")
	if st != "local-lvm" || !hasSize || size != 32<<30 || cdrom {
		t.Fatalf("st=%s size=%d hasSize=%v cdrom=%v", st, size, hasSize, cdrom)
	}
	if volid != "local-lvm:vm-100-disk-0" {
		t.Errorf("volid=%s", volid)
	}
}

func TestParseDiskCDROM(t *testing.T) {
	_, _, _, cdrom, _ := ParseDisk("local:iso/debian.iso,media=cdrom")
	if !cdrom {
		t.Error("expected cdrom=true")
	}
	st, _, _, _, _ := ParseDisk("none,media=cdrom")
	if st != "" {
		t.Errorf("none should yield empty storage, got %q", st)
	}
}

func TestParseDiskUnused(t *testing.T) {
	st, _, hasSize, _, volid := ParseDisk("local-lvm:vm-100-disk-1")
	if st != "local-lvm" || hasSize || volid != "local-lvm:vm-100-disk-1" {
		t.Fatalf("st=%s hasSize=%v volid=%s", st, hasSize, volid)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"32G": 32 << 30, "512M": 512 << 20, "528K": 528 << 10,
		"4M": 4 << 20, "1T": 1 << 40, "1024": 1024,
	}
	for in, want := range cases {
		got, ok := parseSize(in)
		if !ok || got != want {
			t.Errorf("parseSize(%s)=%d,%v want %d", in, got, ok, want)
		}
	}
}

func TestGuestResourcesQEMU(t *testing.T) {
	cfg := map[string]string{
		"sockets":  "2",
		"cores":    "4",
		"memory":   "8192",
		"scsi0":    "local-lvm:vm-100-disk-0,size=32G",
		"efidisk0": "local-lvm:vm-100-disk-1,size=528K",
		"ide2":     "local:iso/x.iso,media=cdrom",
		"unused0":  "local-lvm:vm-100-disk-2",
		"net0":     "virtio,bridge=vmbr0",
		"name":     "x",
	}
	r := GuestResources("qemu", cfg)
	if r.Cores != 8 {
		t.Errorf("cores=%d want 8", r.Cores)
	}
	if r.MemoryMiB != 8192 {
		t.Errorf("memory=%d want 8192", r.MemoryMiB)
	}
	if want := int64(32<<30 + 528<<10); r.Disks["local-lvm"] != want {
		t.Errorf("disk=%d want %d", r.Disks["local-lvm"], want)
	}
	if len(r.Unused) != 1 || r.Unused[0].Volid != "local-lvm:vm-100-disk-2" {
		t.Errorf("unused=%+v", r.Unused)
	}
}

func TestGuestResourcesQEMUDefaults(t *testing.T) {
	r := GuestResources("qemu", map[string]string{})
	if r.Cores != 1 || r.MemoryMiB != 512 {
		t.Errorf("defaults cores=%d mem=%d want 1/512", r.Cores, r.MemoryMiB)
	}
}

func TestGuestResourcesLXC(t *testing.T) {
	cfg := map[string]string{
		"cores":  "2",
		"memory": "1024",
		"rootfs": "local-lvm:subvol-101-disk-0,size=8G",
		"mp0":    "local-lvm:subvol-101-disk-1,size=16G,mp=/data",
	}
	r := GuestResources("lxc", cfg)
	if r.Cores != 2 || r.MemoryMiB != 1024 {
		t.Errorf("cores=%d mem=%d", r.Cores, r.MemoryMiB)
	}
	if want := int64(8<<30 + 16<<30); r.Disks["local-lvm"] != want {
		t.Errorf("disk=%d want %d", r.Disks["local-lvm"], want)
	}
}

func TestUsageDiskGiBCeil(t *testing.T) {
	u := Usage{DiskBytes: map[string]int64{"tank": 32<<30 + 1}}
	if g := u.DiskGiB()["tank"]; g != 33 {
		t.Errorf("DiskGiB=%d want 33 (ceil)", g)
	}
}
