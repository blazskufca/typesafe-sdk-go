package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for use with [errors.Is]. Every error returned by this package
// matches at least one of them.
var (
	// ErrAPI matches any unsuccessful HTTP response, whatever its status.
	ErrAPI = errors.New("typesafe: API error")
	// ErrBadRequest matches HTTP 400.
	ErrBadRequest = errors.New("typesafe: bad request")
	// ErrAuthentication matches HTTP 401.
	ErrAuthentication = errors.New("typesafe: authentication failed")
	// ErrPermissionDenied matches HTTP 403.
	ErrPermissionDenied = errors.New("typesafe: permission denied")
	// ErrNotFound matches HTTP 404.
	ErrNotFound = errors.New("typesafe: not found")
	// ErrUnprocessableEntity matches HTTP 422.
	ErrUnprocessableEntity = errors.New("typesafe: unprocessable entity")
	// ErrRateLimit matches HTTP 429.
	ErrRateLimit = errors.New("typesafe: rate limit exceeded")
	// ErrInternalServer matches HTTP 5xx.
	ErrInternalServer = errors.New("typesafe: internal server error")
	// ErrConnection matches a request that failed without an HTTP response.
	ErrConnection = errors.New("typesafe: connection error")
	// ErrTimeout matches a request that exceeded its timeout. It implies ErrConnection.
	ErrTimeout = errors.New("typesafe: request timed out")
	// ErrInvalidResponse matches a successful response whose body was missing or
	// structurally invalid.
	ErrInvalidResponse = errors.New("typesafe: invalid response data")
	// ErrConfig matches an invalid client or call configuration, such as a missing
	// API key or a non-positive timeout.
	ErrConfig = errors.New("typesafe: invalid configuration")
	// ErrInvalidQuestions matches questions rejected before the request is sent.
	ErrInvalidQuestions = errors.New("typesafe: invalid questions")
)

// APIError is an unsuccessful HTTP response, with its body and request metadata.
//
// Use [errors.Is] with the status sentinels to branch on the kind of failure:
//
//	if errors.Is(err, typesafe.ErrRateLimit) { ... }
type APIError struct {
	// Status is the HTTP response status code.
	Status int
	// Body is the decoded JSON error body, the plain response text, or nil for an
	// empty body.
	Body any
	// RawBody is the undecoded response body.
	RawBody []byte
	// Header holds the response headers.
	Header http.Header
	// Endpoint is the request method and URL without credentials, query, or
	// fragment, when available.
	Endpoint string
	// Message is the server's explanation, extracted from Body.
	Message string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	message := strconv.Itoa(e.Status)
	if e.Message != "" {
		message += " " + e.Message
	}
	if e.Endpoint != "" {
		message = e.Endpoint + ": " + message
	}
	if id := e.RequestID(); id != "" {
		message += " (request_id=" + id + ")"
	}
	return message
}

// RequestID returns the x-typesafe-request-id response header, or "" if absent.
func (e *APIError) RequestID() string { return e.Header.Get(requestIDHeader) }

// RetryAfter reports the server's requested wait, taken from the retry-after-ms or
// Retry-After response headers. The second result is false when neither header
// carries a usable value.
func (e *APIError) RetryAfter() (time.Duration, bool) { return parseRetryAfter(e.Header) }

// Is reports whether the error matches one of the status sentinels.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrAPI:
		return true
	case ErrBadRequest:
		return e.Status == http.StatusBadRequest
	case ErrAuthentication:
		return e.Status == http.StatusUnauthorized
	case ErrPermissionDenied:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrUnprocessableEntity:
		return e.Status == http.StatusUnprocessableEntity
	case ErrRateLimit:
		return e.Status == http.StatusTooManyRequests
	case ErrInternalServer:
		return e.Status >= http.StatusInternalServerError
	}
	return false
}

// ResponseValidationError is a successful HTTP response whose body was missing or
// structurally invalid required data.
type ResponseValidationError struct {
	// FieldPath is the dotted path to the offending field, such as
	// "answers.tone.confidence" or "models[1].name".
	FieldPath string
	// Status is the HTTP response status code.
	Status int
	// Body is the decoded response body.
	Body any
	// RawBody is the undecoded response body.
	RawBody []byte
	// Header holds the response headers.
	Header http.Header
	// Endpoint is the request method and URL, when available.
	Endpoint string
	// Err is the underlying decoding error, if any.
	Err error
}

// Error implements the error interface.
func (e *ResponseValidationError) Error() string {
	message := fmt.Sprintf("%d invalid response data at %q", e.Status, e.FieldPath)
	if e.Endpoint != "" {
		message = e.Endpoint + ": " + message
	}
	if id := e.RequestID(); id != "" {
		message += " (request_id=" + id + ")"
	}
	return message
}

// RequestID returns the x-typesafe-request-id response header, or "" if absent.
func (e *ResponseValidationError) RequestID() string { return e.Header.Get(requestIDHeader) }

// Unwrap returns the underlying decoding error, if any.
func (e *ResponseValidationError) Unwrap() error { return e.Err }

// Is reports whether the error matches [ErrInvalidResponse].
func (e *ResponseValidationError) Is(target error) bool { return target == ErrInvalidResponse }

// ConnectionError is a request that failed without an HTTP response.
type ConnectionError struct {
	// Endpoint is the request method and URL, when available.
	Endpoint string
	// Err is the underlying transport error.
	Err error
}

// Error implements the error interface.
func (e *ConnectionError) Error() string {
	if e.Endpoint != "" {
		return fmt.Sprintf("typesafe: %s: connection error: %v", e.Endpoint, e.Err)
	}
	return fmt.Sprintf("typesafe: connection error: %v", e.Err)
}

// Unwrap returns the underlying transport error.
func (e *ConnectionError) Unwrap() error { return e.Err }

// Is reports whether the error matches [ErrConnection].
func (e *ConnectionError) Is(target error) bool { return target == ErrConnection }

// TimeoutError is a request that exceeded its configured timeout. It satisfies
// [net.Error], so code that already branches on Timeout() keeps working.
type TimeoutError struct {
	// Duration is the timeout that elapsed.
	Duration time.Duration
	// Endpoint is the request method and URL, when available.
	Endpoint string
	// Err is the underlying transport error.
	Err error
}

// Error implements the error interface.
func (e *TimeoutError) Error() string {
	message := fmt.Sprintf("typesafe: request timed out (timeout=%s)", e.Duration)
	if e.Endpoint != "" {
		message = fmt.Sprintf("typesafe: %s: request timed out (timeout=%s)", e.Endpoint, e.Duration)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Timeout implements [net.Error] and always reports true.
func (e *TimeoutError) Timeout() bool { return true }

// Temporary implements the deprecated half of [net.Error] and always reports true.
func (e *TimeoutError) Temporary() bool { return true }

// Unwrap returns the underlying transport error.
func (e *TimeoutError) Unwrap() error { return e.Err }

// Is reports whether the error matches [ErrTimeout] or [ErrConnection]; a timeout
// is a connection failure that took a while.
func (e *TimeoutError) Is(target error) bool {
	return target == ErrTimeout || target == ErrConnection
}

// newAPIError builds the error for an unsuccessful response.
func newAPIError(status int, body any, raw []byte, header http.Header, endpoint string) *APIError {
	return &APIError{
		Status:   status,
		Body:     body,
		RawBody:  raw,
		Header:   header,
		Endpoint: endpoint,
		Message:  errorMessage(body),
	}
}

// errorMessage renders the human-readable part of an error response.
func errorMessage(body any) string {
	if detail := extractMessage(body); detail != "" {
		return detail
	}
	if body == nil {
		return "status code (no body)"
	}
	raw, ok := body.(string)
	if !ok {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Sprint(body)
		}
		raw = string(encoded)
	}
	return truncate(raw, maxErrorBodyLength)
}

// extractMessage pulls the server's explanation out of a decoded error body,
// looking at the "error", "message", and "detail" shapes the API uses.
func extractMessage(body any) string {
	switch value := body.(type) {
	case string:
		return value
	case map[string]any:
		if text, ok := value["error"].(string); ok {
			return text
		}
		if nested, ok := value["error"].(map[string]any); ok {
			if text, ok := nested["message"].(string); ok {
				return text
			}
		}
		if text, ok := value["message"].(string); ok {
			return text
		}
		switch detail := value["detail"].(type) {
		case string:
			return detail
		case map[string]any:
			if text, ok := detail["message"].(string); ok {
				return text
			}
		case []any:
			return validationMessages(detail)
		}
	}
	return ""
}

// validationMessages joins the messages of a FastAPI-style validation detail list,
// prefixing each with its dotted location.
func validationMessages(detail []any) string {
	parts := make([]string, 0, len(detail))
	for _, item := range detail {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		message, ok := entry["msg"].(string)
		if !ok {
			continue
		}
		if location, ok := entry["loc"].([]any); ok {
			if path := joinLocation(location); path != "" {
				message = path + ": " + message
			}
		}
		parts = append(parts, message)
	}
	return strings.Join(parts, "; ")
}

// joinLocation renders a validation location, dropping the leading "body" marker.
func joinLocation(location []any) string {
	segments := make([]string, 0, len(location))
	for _, item := range location {
		if text, ok := item.(string); ok && text == "body" {
			continue
		}
		segments = append(segments, formatScalar(item))
	}
	return strings.Join(segments, ".")
}

// formatScalar renders a decoded JSON scalar the way the API reports it, so whole
// numbers keep their integer form.
func formatScalar(value any) string {
	if number, ok := value.(float64); ok && number == math.Trunc(number) && math.Abs(number) < 1e15 {
		return strconv.FormatInt(int64(number), 10)
	}
	return fmt.Sprint(value)
}

// truncate shortens text to at most limit runes, marking the cut with an ellipsis.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// parseRetryAfter reads the retry-after-ms and Retry-After response headers,
// accepting both delay-seconds and HTTP-date forms.
func parseRetryAfter(header http.Header) (time.Duration, bool) {
	for _, candidate := range [...]struct {
		name  string
		scale time.Duration
	}{{retryAfterMSHeader, time.Millisecond}, {retryAfterHeader, time.Second}} {
		raw := header.Get(candidate.name)
		if raw == "" {
			if _, present := header[http.CanonicalHeaderKey(candidate.name)]; !present {
				continue
			}
			raw = "0"
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			if candidate.name != retryAfterHeader {
				continue
			}
			date, err := http.ParseTime(strings.TrimSpace(raw))
			if err != nil {
				continue
			}
			return max(0, time.Until(date)), true
		}
		if math.IsInf(value, 0) || math.IsNaN(value) {
			continue
		}
		if value < 0 {
			// A negative delay-seconds value means "do not wait for this one".
			if candidate.name == retryAfterHeader {
				return 0, false
			}
			continue
		}
		delay := value * float64(candidate.scale)
		if delay > float64(math.MaxInt64) {
			// A delay too large to represent is no guidance at all.
			continue
		}
		return time.Duration(delay), true
	}
	return 0, false
}

// requestEndpoint describes a request without leaking credentials, query values,
// or fragments.
func requestEndpoint(method string, target *url.URL) string {
	if target == nil {
		return ""
	}
	clean := *target
	clean.User = nil
	clean.RawQuery = ""
	clean.ForceQuery = false
	clean.Fragment = ""
	clean.RawFragment = ""
	return method + " " + clean.String()
}

// configErrorf builds an error that matches [ErrConfig].
func configErrorf(format string, args ...any) error {
	return fmt.Errorf("typesafe: %w: %s", ErrConfig, fmt.Sprintf(format, args...))
}

// questionErrorf builds an error that matches [ErrInvalidQuestions].
func questionErrorf(format string, args ...any) error {
	return fmt.Errorf("typesafe: %w: %s", ErrInvalidQuestions, fmt.Sprintf(format, args...))
}
