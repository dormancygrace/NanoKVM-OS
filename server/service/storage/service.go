package storage

import "sync"

type Service struct {
	mu     sync.Mutex
	remote *remoteSession
}

func NewService() *Service { return &Service{} }
