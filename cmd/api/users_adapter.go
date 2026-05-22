package main

import (
	"github.com/renatokeys/zapfast/internal/users"
)

type usersConnState struct {
	cm *ClientManager
}

func (s usersConnState) Connected(userID string) bool {
	if s.cm == nil {
		return false
	}
	s.cm.RLock()
	defer s.cm.RUnlock()
	c, ok := s.cm.whatsmeowClients[userID]
	return ok && c != nil && c.IsConnected()
}

func (s usersConnState) LoggedIn(userID string) bool {
	if s.cm == nil {
		return false
	}
	s.cm.RLock()
	defer s.cm.RUnlock()
	c, ok := s.cm.whatsmeowClients[userID]
	return ok && c != nil && c.IsLoggedIn()
}

type legacyIDGen struct{}

func (legacyIDGen) Generate() (string, error) { return GenerateRandomID() }

type legacyHMACEncryptor struct{}

func (legacyHMACEncryptor) Encrypt(plain string) ([]byte, error) { return encryptHMACKey(plain) }

func newUsersService(repo users.Repository, cm *ClientManager) *users.Service {
	return users.New(
		repo,
		usersConnState{cm: cm},
		users.WithIDGenerator(legacyIDGen{}),
		users.WithHMACEncryptor(legacyHMACEncryptor{}),
	)
}
