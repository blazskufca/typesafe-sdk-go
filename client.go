package typesafe

import (
	"context"
	"net/http"
	"time"
)

// A Client talks to the TypeSafe AI API. It is safe for concurrent use, and
// holds no resources that need releasing: it reuses connections through its
// [http.Client], which callers can supply with [WithHTTPClient].
type Client struct {
	cfg config

	// Models reaches the models available to the account.
	Models *ModelsService
}

// New creates a client. Options override the environment, which overrides the
// defaults; empty and whitespace-only environment values are ignored.
//
// It fails when no API key is available, or when an option is invalid; both match
// [ErrConfig].
//
//	client, err := typesafe.New(typesafe.WithModel("jev-latest"))
func New(opts ...Option) (*Client, error) {
	cfg, err := resolve(opts)
	if err != nil {
		return nil, err
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.sleep == nil {
		cfg.sleep = wait
	}
	client := &Client{cfg: cfg}
	client.Models = &ModelsService{client: client}
	return client, nil
}

// CloseIdleConnections closes the connections the client is holding open but not
// using. It is never required: the underlying [http.Client] reuses and expires
// connections on its own.
func (c *Client) CloseIdleConnections() { c.cfg.httpClient.CloseIdleConnections() }

// callConfig resolves the configuration for one call.
func (c *Client) callConfig(opts []Option) (config, error) { return c.cfg.with(opts) }

// ModelsService reaches the models endpoint. Use it through [Client.Models].
type ModelsService struct {
	client *Client
}

// List returns the models available to the account.
//
//	models, err := client.Models.List(ctx)
//	for _, model := range models.Models {
//		fmt.Println(model.Name, model.ReleaseDate)
//	}
func (s *ModelsService) List(ctx context.Context, opts ...Option) (*ListModelsResponse, error) {
	cfg, err := s.client.callConfig(opts)
	if err != nil {
		return nil, err
	}
	return send[ListModelsResponse](ctx, cfg, http.MethodGet, modelsPath, nil)
}
