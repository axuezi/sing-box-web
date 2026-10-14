// Package clashapi is a minimal client for sing-box's Clash-compatible API.
package clashapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Client talks to experimental.clash_api.external_controller.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:9090
	Secret  string
	HTTP    *http.Client
}

// Version is the response of GET /version.
type Version struct {
	Version string `json:"version"`
	Premium bool   `json:"premium"`
	Meta    bool   `json:"meta"`
}

// Version returns the running sing-box version.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.getJSON(ctx, "/version", &v)
	return v, err
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clash api %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Connection is one tracked connection.
type Connection struct {
	ID       string `json:"id"`
	Metadata struct {
		Network         string `json:"network"`
		Type            string `json:"type"` // "<inbound type>/<inbound tag>"
		SourceIP        string `json:"sourceIP"`
		DestinationIP   string `json:"destinationIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
	} `json:"metadata"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	Rule     string    `json:"rule"`
}

// Snapshot is GET /connections.
type Snapshot struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
	Memory        int64        `json:"memory"`
}

// Connections returns all open connections and lifetime totals.
func (c *Client) Connections(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	err := c.getJSON(ctx, "/connections", &s)
	return s, err
}

// CloseConnection closes one connection; id "" closes all.
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	path := "/connections"
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("clash api close: %s", resp.Status)
	}
	return nil
}
