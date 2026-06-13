// Package pve is a minimal read-only PVE API client used by the accounting
// engine. It authenticates with the service-account API token
// (uq-proxy@pve, role UQ-ProxyAudit on /) and only ever issues GETs.
package pve

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Member is a guest entry in a pool.
type Member struct {
	VMID int
	Node string
	Type string // "qemu" | "lxc" | "storage" | ...
}

// Client talks to one pveproxy upstream with a fixed API token.
type Client struct {
	base  *url.URL
	token string // "user@realm!tokenid=secret"
	hc    *http.Client
}

// New builds a client. token is the full PVEAPIToken value
// "uq-proxy@pve!audit=SECRET". tlsCfg controls upstream verification.
func New(base *url.URL, token string, tlsCfg *tls.Config) *Client {
	return &Client{
		base:  base,
		token: token,
		hc: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
}

func (c *Client) get(path string, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pve GET %s: %s", path, resp.Status)
	}
	var wrap struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		return fmt.Errorf("pve GET %s: decode: %w", path, err)
	}
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil
	}
	return json.Unmarshal(wrap.Data, out)
}

// PoolMembers returns the guests (and other members) of a pool.
func (c *Client) PoolMembers(poolid string) ([]Member, error) {
	var data struct {
		Members []struct {
			VMID int    `json:"vmid"`
			Node string `json:"node"`
			Type string `json:"type"`
		} `json:"members"`
	}
	if err := c.get("/api2/json/pools/"+url.PathEscape(poolid), &data); err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(data.Members))
	for _, m := range data.Members {
		out = append(out, Member{VMID: m.VMID, Node: m.Node, Type: m.Type})
	}
	return out, nil
}

// GuestConfig returns the current config of a guest as string values. kind is
// "qemu" or "lxc".
func (c *Client) GuestConfig(node, kind string, vmid int) (map[string]string, error) {
	var raw map[string]any
	p := fmt.Sprintf("/api2/json/nodes/%s/%s/%d/config",
		url.PathEscape(node), kind, vmid)
	if err := c.get(p, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = stringify(v)
	}
	return out, nil
}

// StorageContent returns a volid -> size(bytes) map for a node's storage,
// used to size unused[n] disks that carry no size in the guest config.
func (c *Client) StorageContent(node, storage string) (map[string]int64, error) {
	var data []struct {
		Volid string `json:"volid"`
		Size  int64  `json:"size"`
	}
	p := fmt.Sprintf("/api2/json/nodes/%s/storage/%s/content",
		url.PathEscape(node), url.PathEscape(storage))
	if err := c.get(p, &data); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(data))
	for _, d := range data {
		out[d.Volid] = d.Size
	}
	return out, nil
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
