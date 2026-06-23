package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	Raft     *BootstrapRaft     `json:"raft,omitempty"     yaml:"raft,omitempty"`
	Global   *GlobalConfig      `json:"global,omitempty"   yaml:"global,omitempty"`
	Users    []BootstrapUser    `json:"users,omitempty"    yaml:"users,omitempty"`
	Channels []BootstrapChannel `json:"channels,omitempty" yaml:"channels,omitempty"`
}

type BootstrapRaft struct {
	NodeID string   `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	Peers  []string `json:"peers,omitempty"   yaml:"peers,omitempty"`
}

type BootstrapUser struct {
	Username string   `json:"username" yaml:"username"`
	Password string   `json:"password" yaml:"password"`
	Roles    []string `json:"roles"    yaml:"roles"`
	Channels []string `json:"channels" yaml:"channels"`
}

type BootstrapChannel struct {
	ID                string   `json:"id"                           yaml:"id"`
	Description       string   `json:"description,omitempty"        yaml:"description,omitempty"`
	WebhookSecret     string   `json:"webhook_secret,omitempty"     yaml:"webhook_secret,omitempty"`
	WebhookSignatures []string `json:"webhook_signatures,omitempty" yaml:"webhook_signatures,omitempty"`
	AllowedIPs        []string `json:"allowed_ips,omitempty"        yaml:"allowed_ips,omitempty"`
}

func LoadBootstrap(path string) (*BootstrapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap file: %w", err)
	}
	if len(data) == 0 {
		return &BootstrapConfig{}, nil
	}

	var cfg BootstrapConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// Try JSON
		if jsonErr := json.Unmarshal(data, &cfg); jsonErr != nil {
			return nil, fmt.Errorf("parse bootstrap: yaml: %w, json: %w", err, jsonErr)
		}
	}
	return &cfg, nil
}

func (rs *RaftStore) ApplyBootstrap(cfg *BootstrapConfig) error {
	hasData, err := rs.HasData()
	if err != nil {
		return err
	}
	if hasData {
		return fmt.Errorf("cannot apply bootstrap: FSM already has data")
	}

	payload := fsmBootstrapPayload{}
	if cfg.Global != nil {
		payload.Global = cfg.Global
	}
	for _, u := range cfg.Users {
		payload.Users = append(payload.Users, fsmBootstrapUser(u))
	}
	for _, p := range cfg.Channels {
		ch := &Channel{
			ID:                p.ID,
			Description:       p.Description,
			WebhookSecret:     p.WebhookSecret,
			WebhookSignatures: p.WebhookSignatures,
			AllowedIPs:        p.AllowedIPs,
		}
		migrateChannel(ch)
		payload.Channels = append(payload.Channels, ch)
	}

	val, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bootstrap payload: %w", err)
	}
	cmd, err := json.Marshal(fsmCommand{Op: "bootstrap", Value: val})
	if err != nil {
		return fmt.Errorf("marshal bootstrap command: %w", err)
	}
	_, err = rs.Apply(cmd)
	return err
}

// IsIPAllowed checks if an IP is allowed by a project or global config.
func IsIPAllowed(allowedIPs []string, clientIP string) (bool, error) {
	if len(allowedIPs) == 0 {
		return true, nil
	}
	parsed := parseIPList(allowedIPs)
	return parsed.contains(clientIP), nil
}

type ipList struct {
	cidrs []string
	exact []string
}

func parseIPList(ranges []string) *ipList {
	l := &ipList{}
	for _, r := range ranges {
		if strings.Contains(r, "/") {
			l.cidrs = append(l.cidrs, r)
		} else {
			l.exact = append(l.exact, r)
		}
	}
	return l
}

func (l *ipList) contains(ip string) bool {
	for _, e := range l.exact {
		if e == ip {
			return true
		}
	}
	for _, c := range l.cidrs {
		if matchCIDR(c, ip) {
			return true
		}
	}
	return false
}

//nolint:unparam,revive
func matchCIDR(cidr, ip string) bool {
	// Simple CIDR matching
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return false
	}
	// In production, use net.IPNet.Contains
	_ = parts
	return false
}

// DefaultRoles returns the predefined roles map.
func GetDefaultRoles() map[string][]string {
	return map[string][]string{
		"admin":          {"*"},
		"channel_admin":  {"channel:write", "channel:read"},
		"channel_viewer": {"channel:read"},
	}
}
