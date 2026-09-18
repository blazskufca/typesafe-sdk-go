package typesafe

import "time"

// Environment variables read when the corresponding [Option] is not supplied.
// Empty or whitespace-only values are ignored.
const (
	// APIKeyEnv holds the API key.
	APIKeyEnv = "TYPESAFE_API_KEY"
	// BaseURLEnv holds the API base URL.
	BaseURLEnv = "TYPESAFE_BASE_URL"
	// DefaultModelEnv holds the default model name.
	DefaultModelEnv = "TYPESAFE_DEFAULT_MODEL"
	// LogLevelEnv holds the log level ("debug", "info", "warn", "error", "off")
	// used to build the default logger. See [WithLogger].
	LogLevelEnv = "TYPESAFE_LOG_LEVEL"
)

// Client defaults.
const (
	// DefaultBaseURL is the API root used when none is configured.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the model used when none is configured.
	DefaultModel = "jev-latest"
	// DefaultTimeout is the timeout applied to each HTTP attempt.
	DefaultTimeout = 10 * time.Second
)

// Version is the SDK version reported in the User-Agent header.
const Version = "0.1.0"

const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"
	sdkName       = "typesafe-sdk-go"

	jsonContentType = "application/json"

	authorizationHeader = "Authorization"
	acceptHeader        = "Accept"
	contentTypeHeader   = "Content-Type"
	userAgentHeader     = "User-Agent"
	sdkHeader           = "X-Typesafe-Sdk"
	runtimeHeader       = "X-Typesafe-Runtime"
	retryCountHeader    = "X-Typesafe-Retry-Count"
	requestIDHeader     = "X-Typesafe-Request-Id"
	retryAfterHeader    = "Retry-After"
	retryAfterMSHeader  = "Retry-After-Ms"

	maxErrorBodyLength = 200
)
