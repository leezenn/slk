package cmd

import (
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
)

// Dependencies contains the process-boundary seams commands need for isolation.
type Dependencies struct {
	Credentials auth.Store
	NewClient   func(token string) *api.Client
	Now         func() time.Time
}

// DefaultDependencies returns the concrete local process dependencies.
func DefaultDependencies() Dependencies {
	return Dependencies{
		Credentials: auth.NewStore(),
		NewClient:   api.NewClient,
		Now:         time.Now,
	}
}

func (d Dependencies) credentialStore() (auth.Store, error) {
	if d.Credentials == nil {
		return nil, internalError()
	}
	return d.Credentials, nil
}

func (d Dependencies) client(token string) (*api.Client, error) {
	if d.NewClient == nil {
		return nil, internalError()
	}
	client := d.NewClient(token)
	if client == nil {
		return nil, internalError()
	}
	return client, nil
}

func (d Dependencies) now() (time.Time, error) {
	if d.Now == nil {
		return time.Time{}, internalError()
	}
	return d.Now(), nil
}
