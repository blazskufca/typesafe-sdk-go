package typesafe

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// An Option configures a [Client] or a single call. The same options work in
// both places: what [New] receives becomes the client default, and what a
// request method receives applies to that call only.
type Option func(*config) error

// config holds every resolved setting a request needs.
type config struct {
	apiKey     string
	baseURL    string
	model      string
	timeout    time.Duration
	header     http.Header
	retry      RetryPolicy
	httpClient *http.Client
	logger     *slog.Logger
	extraBody  map[string]any
	dotenv     []dotenvFile

	// now and sleep are the seams the retry loop measures and waits through, so
	// that tests can drive it without real time passing.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// WithAPIKey sets the API key. It defaults to the TYPESAFE_API_KEY environment
// variable.
func WithAPIKey(key string) Option {
	return func(c *config) error {
		if strings.TrimSpace(key) == "" {
			return configErrorf("the API key must not be empty")
		}
		c.apiKey = key
		return nil
	}
}

// WithBaseURL sets the API root. It defaults to the TYPESAFE_BASE_URL environment
// variable, then to [DefaultBaseURL].
func WithBaseURL(baseURL string) Option {
	return func(c *config) error {
		trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if trimmed == "" {
			return configErrorf("the base URL must not be empty")
		}
		if _, err := url.Parse(trimmed); err != nil {
			return configErrorf("the base URL %q is not a valid URL: %v", baseURL, err)
		}
		c.baseURL = trimmed
		return nil
	}
}

// WithModel sets the model to answer questions with. It defaults to the
// TYPESAFE_DEFAULT_MODEL environment variable, then to [DefaultModel].
func WithModel(model string) Option {
	return func(c *config) error {
		if strings.TrimSpace(model) == "" {
			return configErrorf("the model must not be empty")
		}
		c.model = model
		return nil
	}
}

// WithTimeout sets the timeout for a single HTTP attempt, retries excluded. It
// defaults to [DefaultTimeout]. Use [RetryPolicy.Timeout] to bound a call as a
// whole, or cancel the context to bound it from the outside.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) error {
		if timeout <= 0 {
			return configErrorf("the timeout must be positive, got %s", timeout)
		}
		c.timeout = timeout
		return nil
	}
}

// WithHeader adds a request header. Authentication, SDK identification, Accept,
// and Content-Type are set by the SDK and cannot be overridden.
func WithHeader(name, value string) Option {
	return func(c *config) error {
		c.header = cloneHeader(c.header)
		c.header.Set(name, value)
		return nil
	}
}

// WithHeaders adds request headers, replacing any already set under the same
// names. Authentication, SDK identification, Accept, and Content-Type are set by
// the SDK and cannot be overridden.
func WithHeaders(header http.Header) Option {
	return func(c *config) error {
		c.header = cloneHeader(c.header)
		for name, values := range header {
			c.header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
		return nil
	}
}

// WithRetryPolicy sets the retry behavior. It defaults to [DefaultRetryPolicy].
// Pass a zero [RetryPolicy] to disable retries.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(c *config) error {
		if err := policy.validate(); err != nil {
			return err
		}
		c.retry = policy.clone()
		return nil
	}
}

// WithHTTPClient sets the underlying HTTP client, letting callers supply their own
// transport, proxy, or connection pool. Per-attempt timeouts are applied through
// the request context, so an [http.Client.Timeout] bounds the attempt too, whichever
// elapses first.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) error {
		if client == nil {
			return configErrorf("the HTTP client must not be nil")
		}
		c.httpClient = client
		return nil
	}
}

// WithLogger sets the logger. The SDK logs requests and responses at debug level,
// and retries, failures, and unrecognized answers at info and warn level; secret
// headers are redacted, request and response bodies are not.
//
// Without this option the SDK builds a logger from the TYPESAFE_LOG_LEVEL
// environment variable ("debug", "info", "warn", "error", "off"), and stays silent
// when it is unset.
func WithLogger(logger *slog.Logger) Option {
	return func(c *config) error {
		if logger == nil {
			return configErrorf("the logger must not be nil")
		}
		c.logger = logger
		return nil
	}
}

// WithExtraBody adds top-level fields to a System One request body, shallow-merged
// over it after state, model, and questions are set. It is an escape hatch for
// fields the API accepts but this SDK does not model yet; a key that collides with
// a modeled field replaces it. Other endpoints ignore it.
func WithExtraBody(fields map[string]any) Option {
	return func(c *config) error {
		c.extraBody = maps.Clone(fields)
		return nil
	}
}

// resolve builds the client configuration from the supplied options, the
// environment, any .env file, and the defaults, in that order.
func resolve(opts []Option) (config, error) {
	cfg := config{
		header:     http.Header{},
		retry:      DefaultRetryPolicy(),
		httpClient: &http.Client{},
	}
	if err := cfg.apply(opts); err != nil {
		return config{}, err
	}
	dotenv, err := loadDotenv(cfg.dotenv)
	if err != nil {
		return config{}, err
	}
	// A setting no option supplied comes from the environment, then from a .env
	// file, then from the default.
	lookup := func(name string) string {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
		return strings.TrimSpace(dotenv[name])
	}
	if cfg.apiKey == "" {
		cfg.apiKey = lookup(APIKeyEnv)
	}
	if cfg.apiKey == "" {
		return config{}, configErrorf(
			"no API key was provided; pass WithAPIKey, set the %s environment variable, or load a .env file with WithDotenv", APIKeyEnv)
	}
	if cfg.baseURL == "" {
		cfg.baseURL = or(lookup(BaseURLEnv), DefaultBaseURL)
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	if cfg.model == "" {
		cfg.model = or(lookup(DefaultModelEnv), DefaultModel)
	}
	if cfg.timeout == 0 {
		cfg.timeout = DefaultTimeout
	}
	if cfg.logger == nil {
		cfg.logger = levelLogger(lookup(LogLevelEnv))
	}
	return cfg, nil
}

// apply runs the options against the configuration.
func (c *config) apply(opts []Option) error {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(c); err != nil {
			return err
		}
	}
	return nil
}

// with returns a copy of the configuration with the per-call options applied.
func (c config) with(opts []Option) (config, error) {
	if len(opts) == 0 {
		return c, nil
	}
	clone := c
	clone.retry = c.retry.clone()
	if err := clone.apply(opts); err != nil {
		return config{}, err
	}
	if len(clone.dotenv) != len(c.dotenv) {
		return config{}, configErrorf("WithDotenv configures a client; pass it to New rather than to a call")
	}
	return clone, nil
}

// or returns value, or fallback when value is empty.
func or(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// cloneHeader copies a header so that per-call options never mutate the client's.
func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return http.Header{}
	}
	return header.Clone()
}
