package main

import (
	"context"

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
		users.WithUpdateHook(updateHookFor(cm)),
	)
}

func updateHookFor(_ *ClientManager) users.UpdateHook {
	return func(_ context.Context, ev users.UpdateResult) {
		if userinfocache != nil {
			userinfocache.Delete(ev.OldToken)
			if ev.NewToken != "" && ev.NewToken != ev.OldToken {
				userinfocache.Delete(ev.NewToken)
			}
		}
		if ev.S3Config != nil {
			s3mgr := GetS3Manager()
			if s3mgr == nil {
				return
			}
			if ev.S3Config.Enabled {
				s3Config := &S3Config{
					Enabled:       ev.S3Config.Enabled,
					Endpoint:      ev.S3Config.Endpoint,
					Region:        ev.S3Config.Region,
					Bucket:        ev.S3Config.Bucket,
					AccessKey:     ev.S3Config.AccessKey,
					SecretKey:     ev.S3Config.SecretKey,
					PathStyle:     ev.S3Config.PathStyle,
					PublicURL:     ev.S3Config.PublicURL,
					MediaDelivery: ev.S3Config.MediaDelivery,
					RetentionDays: ev.S3Config.RetentionDays,
				}
				_ = s3mgr.InitializeS3Client(ev.UserID, s3Config)
			} else {
				s3mgr.RemoveClient(ev.UserID)
			}
		}
	}
}
