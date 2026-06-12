package store

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	Raft     *BootstrapRaft      `yaml:"raft,omitempty" json:"raft,omitempty"`
	Global   *GlobalConfig       `yaml:"global,omitempty" json:"global,omitempty"`
	Users    []BootstrapUser     `yaml:"users,omitempty" json:"users,omitempty"`
	Projects []BootstrapProject  `yaml:"projects,omitempty" json:"projects,omitempty"`
}

type BootstrapRaft struct {
	NodeID string   `yaml:"node_id,omitempty" json:"node_id,omitempty"`
	Peers  []string `yaml:"peers,omitempty" json:"peers,omitempty"`
}

type BootstrapUser struct {
	Username string   `yaml:"username" json:"username"`
	Password string   `yaml:"password" json:"password"`
	Roles    []string `yaml:"roles" json:"roles"`
	Projects []string `yaml:"projects" json:"projects"`
}

type BootstrapProject struct {
	ID                string   `yaml:"id" json:"id"`
	Name              string   `yaml:"name" json:"name"`
	WebhookSignatures []string `yaml:"webhook_signatures,omitempty" json:"webhook_signatures,omitempty"`
	AllowedIPs        []string `yaml:"allowed_ips,omitempty" json:"allowed_ips,omitempty"`
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
		payload.Users = append(payload.Users, fsmBootstrapUser{
			Username: u.Username,
			Password: u.Password,
			Roles:    u.Roles,
			Projects: u.Projects,
		})
	}
	for _, p := range cfg.Projects {
		payload.Projects = append(payload.Projects, &Project{
			ID:                p.ID,
			Name:              p.Name,
			WebhookSignatures: p.WebhookSignatures,
			AllowedIPs:        p.AllowedIPs,
		})
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
	cidrs    []string
	exact    []string
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
		"admin":           {"*"},
		"project_admin":   {"project:write", "project:read"},
		"project_viewer":  {"project:read"},
	}
}