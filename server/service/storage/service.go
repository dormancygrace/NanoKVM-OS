package storage

import "sync"

type Service struct {
	mu          sync.Mutex
	remote      *remoteSession
	remoteKind  string
	remoteError string
	netboot     *netbootSession
	// netbootOp serializes the slow network boot actions (downloads, enable,
	// disable) without holding mu across them.
	netbootOp sync.Mutex
}

func NewService() *Service { return &Service{} }
