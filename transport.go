package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// runtimeDescription identifies the Go runtime in the X-Typesafe-Runtime header.
var runtimeDescription = fmt.Sprintf("go/%s (%s; %s)", strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS, runtime.GOARCH)

// userAgent identifies the SDK in the User-Agent and X-Typesafe-Sdk headers.
var userAgent = sdkName + "/" + Version

// send performs one API call: it sends the request, retries the failures the
// policy allows, and decodes the successful response into T.
func send[T any](ctx context.Context, cfg config, method, path string, body []byte) (*T, error) {
	target, err := url.Parse(cfg.baseURL + path)
	if err != nil {
		return nil, configErrorf("the base URL %q is not a valid URL: %v", cfg.baseURL, err)
	}
	call := &call{cfg: cfg, method: method, url: target, endpoint: requestEndpoint(method, target), body: body}
	call.header = call.headers()

	deadline, bounded := call.budget()
	var lastErr error
	for attempt := 0; ; attempt++ {
		result, err := attemptCall[T](ctx, call, attempt)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt >= cfg.retry.MaxRetries || !cfg.retry.retryable(err) || ctx.Err() != nil {
			return nil, err
		}
		delay := cfg.retry.delay(attempt+1, err)
		if bounded && !cfg.now().Add(delay).Before(deadline) {
			// The next attempt would start at or after the retry budget runs out.
			cfg.logger.LogAttrs(ctx, slog.LevelInfo, "typesafe: retry budget exhausted",
				slog.String("endpoint", call.endpoint), slog.Int("attempts", attempt+1))
			return nil, err
		}
		cfg.logger.LogAttrs(ctx, slog.LevelInfo, "typesafe: retrying",
			slog.String("endpoint", call.endpoint), slog.Int("attempt", attempt+1),
			slog.Duration("delay", delay), slog.String("cause", err.Error()))
		if err := cfg.sleep(ctx, delay); err != nil {
			return nil, errors.Join(lastErr, err)
		}
	}
}

// call is one prepared request, reused across the attempts that retry it.
type call struct {
	cfg      config
	method   string
	url      *url.URL
	endpoint string
	header   http.Header
	body     []byte
}

// headers merges the configured headers with the ones the SDK controls. The SDK's
// come last: authentication, identification, and content negotiation cannot be
// overridden.
func (c *call) headers() http.Header {
	header := cloneHeader(c.cfg.header)
	header.Del(retryCountHeader)
	header.Set(authorizationHeader, "Bearer "+c.cfg.apiKey)
	header.Set(acceptHeader, jsonContentType)
	header.Set(userAgentHeader, userAgent)
	header.Set(sdkHeader, userAgent)
	header.Set(runtimeHeader, runtimeDescription)
	if c.body != nil {
		header.Set(contentTypeHeader, jsonContentType)
	}
	return header
}

// budget returns when the retry budget runs out, and whether there is one.
func (c *call) budget() (time.Time, bool) {
	if c.cfg.retry.Timeout <= 0 {
		return time.Time{}, false
	}
	return c.cfg.now().Add(c.cfg.retry.Timeout), true
}

// attemptCall sends the request once and decodes what comes back.
func attemptCall[T any](ctx context.Context, c *call, attempt int) (*T, error) {
	cfg := c.cfg
	header := c.header
	if attempt > 0 {
		header = c.header.Clone()
		header.Set(retryCountHeader, strconv.Itoa(attempt))
	}

	attemptCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(attemptCtx, c.method, c.url.String(), bodyReader(c.body))
	if err != nil {
		return nil, configErrorf("could not build the request: %v", err)
	}
	request.Header = header
	request.ContentLength = int64(len(c.body))

	cfg.logger.LogAttrs(ctx, slog.LevelDebug, "typesafe: request",
		slog.String("endpoint", c.endpoint), slog.Any("headers", redacted(header)), slog.String("body", string(c.body)))

	started := cfg.now()
	response, err := cfg.httpClient.Do(request)
	if err != nil {
		return nil, c.transportError(ctx, attemptCtx, err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, c.transportError(ctx, attemptCtx, err)
	}

	cfg.logger.LogAttrs(ctx, slog.LevelInfo, "typesafe: response",
		slog.String("endpoint", c.endpoint), slog.Int("status", response.StatusCode),
		slog.Duration("elapsed", cfg.now().Sub(started)), slog.String("request_id", response.Header.Get(requestIDHeader)))
	cfg.logger.LogAttrs(ctx, slog.LevelDebug, "typesafe: response body",
		slog.String("endpoint", c.endpoint), slog.Any("headers", redacted(response.Header)), slog.String("body", string(raw)))

	return parseResponse[T](ctx, c, response, raw)
}

// transportError explains a request that never produced a response.
func (c *call) transportError(ctx, attemptCtx context.Context, err error) error {
	if ctx.Err() != nil {
		// The caller gave up: report their cancellation, not ours.
		return fmt.Errorf("typesafe: %s: %w", c.endpoint, ctx.Err())
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || isTimeout(err) {
		return &TimeoutError{Duration: c.cfg.timeout, Endpoint: c.endpoint, Err: err}
	}
	return &ConnectionError{Endpoint: c.endpoint, Err: err}
}

// isTimeout reports whether a transport error was a timeout of its own.
func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// parseResponse turns a response into T, or into the error it describes.
func parseResponse[T any](ctx context.Context, c *call, response *http.Response, raw []byte) (*T, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newAPIError(response.StatusCode, decodeBody(raw), raw, response.Header, c.endpoint)
	}
	result := new(T)
	if err := json.Unmarshal(raw, result); err != nil {
		return nil, c.validationError(response, raw, err)
	}
	meta := ResponseMetadata{
		RequestID: response.Header.Get(requestIDHeader),
		Status:    response.StatusCode,
		Header:    response.Header,
		RawBody:   raw,
	}
	if setter, ok := any(result).(metadataSetter); ok {
		setter.setMetadata(meta)
	}
	if systemOne, ok := any(result).(*SystemOneResponse); ok {
		for name, kind := range systemOne.unrecognized {
			// Forward compatibility: a future answer type is skipped, not fatal.
			c.cfg.logger.LogAttrs(ctx, slog.LevelWarn, "typesafe: ignoring answer with unrecognized type",
				slog.String("question", name), slog.String("type", kind))
		}
	}
	return result, nil
}

// validationError describes a successful response the SDK could not decode.
func (c *call) validationError(response *http.Response, raw []byte, err error) error {
	path := ""
	var field *fieldError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &field):
		path = field.path
	case errors.As(err, &typeErr):
		path = typeErr.Field
	}
	return &ResponseValidationError{
		FieldPath: path,
		Status:    response.StatusCode,
		Body:      decodeBody(raw),
		RawBody:   raw,
		Header:    response.Header,
		Endpoint:  c.endpoint,
		Err:       err,
	}
}

// decodeBody decodes a body for reporting: JSON when it parses, the text when it
// does not, and nil when there is nothing there.
func decodeBody(raw []byte) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return strings.ToValidUTF8(string(raw), "�")
	}
	return decoded
}

// bodyReader returns a fresh reader over the request body, so that every attempt
// sends it in full.
func bodyReader(body []byte) io.Reader {
	if body == nil {
		return nil
	}
	return bytes.NewReader(body)
}

// encodeBody serializes a request body, blaming the caller's value when it cannot.
func encodeBody(body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("typesafe: %w: the request body could not be encoded as JSON: %w", ErrConfig, err)
	}
	return encoded, nil
}

// wait sleeps for the delay unless the context ends first.
func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
