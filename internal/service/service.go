// Package service contains the application business logic. It depends only on
// the domain package (entities and repository interfaces), never on
// internal/repository or internal/handler.
package service

import (
	"github.com/webcenter-fr/gohookbridge/internal/domain"
)

// Service is the single application service layer: every business operation
// goes through it, backed by a domain.Repository implementation wired in by
// the composition root (internal/server).
type Service struct {
	repo     domain.Repository
	notifier domain.ChannelChangeNotifier
}

func NewService(repo domain.Repository, notifier domain.ChannelChangeNotifier) *Service {
	return &Service{repo: repo, notifier: notifier}
}
