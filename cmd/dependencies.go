package cmd

import (
	"context"
	"io"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/leezenn/slk/internal/profile"
)

// Dependencies contains the process-boundary seams commands need for isolation.
type Dependencies struct {
	Credentials    auth.Store
	Configuration  config.Store
	Profiles       profile.Store
	NewClient      func(token string) *api.Client
	ValidateToken  func(context.Context, string, io.Writer) (*api.AuthTestResult, error)
	ActiveIdentity *config.Identity
	ActiveToken    string
	Now            func() time.Time
}

// DefaultDependencies returns the concrete local process dependencies.
func DefaultDependencies() Dependencies {
	return Dependencies{
		Credentials:   auth.NewStore(),
		Configuration: config.NewStore(),
		Profiles:      profile.NewStore(),
		NewClient:     api.NewClient,
		ValidateToken: func(ctx context.Context, token string, errOut io.Writer) (*api.AuthTestResult, error) {
			client := api.NewClient(token)
			client.SetContext(ctx)
			client.SetErrorWriter(errOut)
			return client.AuthTest()
		},
		Now: time.Now,
	}
}

func (d Dependencies) credentialStore() (auth.Store, error) {
	if d.Credentials == nil {
		return nil, internalError()
	}
	return d.Credentials, nil
}

func (d Dependencies) configStore() (config.Store, error) {
	if d.Configuration == nil {
		return nil, internalError()
	}
	return d.Configuration, nil
}

func (d Dependencies) profileStore() (profile.Store, error) {
	if d.Profiles == nil {
		return nil, internalError()
	}
	return d.Profiles, nil
}

func (d Dependencies) config() (config.Settings, error) {
	store, err := d.configStore()
	if err != nil {
		return config.Settings{}, err
	}
	document, err := store.Load()
	if err != nil {
		return config.Settings{}, configLoadError(err)
	}
	return document.Effective(), nil
}

func (d Dependencies) validateToken(ctx context.Context, token string, errOut io.Writer) (*api.AuthTestResult, error) {
	if d.ValidateToken == nil {
		return nil, internalError()
	}
	return d.ValidateToken(ctx, token, errOut)
}

func (d Dependencies) bindIdentity(token string, result *api.AuthTestResult) (Dependencies, config.Document, config.Preferences, error) {
	if result == nil {
		return d, config.Document{}, config.Preferences{}, internalError()
	}
	identity, err := config.NewIdentity(result.TeamID, result.UserID)
	if err != nil {
		return d, config.Document{}, config.Preferences{}, identityValidationError(err)
	}
	store, err := d.configStore()
	if err != nil {
		return d, config.Document{}, config.Preferences{}, err
	}
	document, err := store.Load()
	if err != nil {
		return d, config.Document{}, config.Preferences{}, configLoadError(err)
	}
	preferences, changed, err := document.BindIdentity(identity)
	if err == nil && changed {
		err = store.Save(document)
	}
	if err != nil {
		return d, config.Document{}, config.Preferences{}, identityConfigError(err)
	}
	d.ActiveIdentity = &identity
	d.ActiveToken = token
	return d, document, preferences, nil
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
