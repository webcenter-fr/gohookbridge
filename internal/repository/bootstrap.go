package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/webcenter-fr/gohookbridge/internal/domain"
	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	Raft     *BootstrapRaft       `json:"raft,omitempty"     yaml:"raft,omitempty"`
	Global   *domain.GlobalConfig `json:"global,omitempty"   yaml:"global,omitempty"`
	Users    []BootstrapUser      `json:"users,omitempty"    yaml:"users,omitempty"`
	Channels []BootstrapChannel   `json:"channels,omitempty" yaml:"channels,omitempty"`
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

func (rs *RaftStore) ApplyBootstrap(ctx context.Context, cfg *BootstrapConfig) error {
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
		ch := &domain.Channel{
			ID:                p.ID,
			Description:       p.Description,
			WebhookSecret:     p.WebhookSecret,
			WebhookSignatures: p.WebhookSignatures,
			AllowedIPs:        p.AllowedIPs,
		}
		domain.MigrateChannel(ch)
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
