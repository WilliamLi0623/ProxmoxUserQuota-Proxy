package pve

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestClient(t *testing.T) {
	const token = "uq-proxy@pve!t=secret"
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/pools/uq-x", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken="+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":{"members":[` +
			`{"vmid":100,"node":"n1","type":"qemu"},` +
			`{"vmid":0,"node":"n1","type":"storage","storage":"local"}]}}`))
	})
	mux.HandleFunc("/api2/json/nodes/n1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"cores":4,"memory":4096,` +
			`"scsi0":"tank:vm-100-disk-0,size=32G","name":"x"}}`))
	})
	mux.HandleFunc("/api2/json/nodes/n1/storage/tank/content", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"volid":"tank:vm-100-disk-0","size":34359738368}]}`))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	base, _ := url.Parse(srv.URL)
	c := New(base, token, &tls.Config{InsecureSkipVerify: true})

	members, err := c.PoolMembers("uq-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].VMID != 100 || members[0].Type != "qemu" {
		t.Fatalf("members=%+v", members)
	}

	cfg, err := c.GuestConfig("n1", "qemu", 100)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["cores"] != "4" || cfg["memory"] != "4096" ||
		cfg["scsi0"] != "tank:vm-100-disk-0,size=32G" {
		t.Fatalf("cfg=%+v", cfg)
	}

	cont, err := c.StorageContent("n1", "tank")
	if err != nil {
		t.Fatal(err)
	}
	if cont["tank:vm-100-disk-0"] != 34359738368 {
		t.Errorf("content=%+v", cont)
	}
}

func TestClientAuthFailure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	c := New(base, "bad", &tls.Config{InsecureSkipVerify: true})
	if _, err := c.PoolMembers("uq-x"); err == nil {
		t.Error("expected error on 401")
	}
}
