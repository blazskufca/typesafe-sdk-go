package typesafe

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// RetryPolicy configures how a call retries failed attempts.
//
// The zero value disables retries; start from [DefaultRetryPolicy] and adjust
// the fields you care about:
//
//	policy := typesafe.DefaultRetryPolicy()
//	policy.MaxRetries = 5
//	client, err := typesafe.New(typesafe.WithRetryPolicy(policy))
type RetryPolicy struct {
	// MaxRetries is the number of retries after the initial attempt; 0 disables
	// retries.
	MaxRetries int

	// BackoffInitial is the first backoff delay, doubled on each attempt up to
	// BackoffMax; zero disables backoff.
	BackoffInitial time.Duration

	// BackoffMax caps the backoff delay; zero disables backoff.
	BackoffMax time.Duration

	// BackoffJitter is the fraction of each backoff delay randomly subtracted,
	// between 0 and 1.
	BackoffJitter float64

	// RetryStatuses lists the HTTP status codes that are retried.
	RetryStatuses []int

	// RespectRetryAfter honors the Retry-After and retry-after-ms response headers
	// in place of the computed backoff.
	RespectRetryAfter bool

	// RetryConnectionErrors retries a request that could not reach or read from the
	// server, reported as [*ConnectionError].
	RetryConnectionErrors bool

	// RetryTimeouts retries a request that exceeded its timeout, reported as
	// [*TimeoutError].
	RetryTimeouts bool

	// Retry is an optional predicate called with the failure; returning true
	// retries it, in addition to the rules above.
	Retry func(error) bool

	// Timeout is the total retry budget per call, including the initial attempt and
	// the delays between attempts; zero disables the limit. A retry whose delay
	// would reach the budget is not attempted, and the last error is returned.
	Timeout time.Duration
}

// DefaultRetryPolicy returns the policy used when a client configures none: two
// retries with exponential backoff, honoring Retry-After, within a 30s budget.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:            2,
		BackoffInitial:        500 * time.Millisecond,
		BackoffMax:            5 * time.Second,
		BackoffJitter:         0.25,
		RetryStatuses:         defaultRetryStatuses(),
		RespectRetryAfter:     true,
		RetryConnectionErrors: true,
		RetryTimeouts:         true,
		Timeout:               30 * time.Second,
	}
}

// defaultRetryStatuses returns 408, 429, and every 5xx status.
func defaultRetryStatuses() []int {
	statuses := make([]int, 0, 102)
	statuses = append(statuses, 408, 429)
	for status := 500; status < 600; status++ {
		statuses = append(statuses, status)
	}
	return statuses
}

// validate reports whether the policy's numbers are usable.
func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return configErrorf("MaxRetries must not be negative, got %d", p.MaxRetries)
	}
	if p.BackoffInitial < 0 {
		return configErrorf("BackoffInitial must not be negative, got %s", p.BackoffInitial)
	}
	if p.BackoffMax < 0 {
		return configErrorf("BackoffMax must not be negative, got %s", p.BackoffMax)
	}
	if math.IsNaN(p.BackoffJitter) || p.BackoffJitter < 0 || p.BackoffJitter > 1 {
		return configErrorf("BackoffJitter must be between zero and one, got %v", p.BackoffJitter)
	}
	if p.Timeout < 0 {
		return configErrorf("Timeout must not be negative, got %s", p.Timeout)
	}
	return nil
}

// clone returns a copy that no longer shares its status slice with p.
func (p RetryPolicy) clone() RetryPolicy {
	p.RetryStatuses = slices.Clone(p.RetryStatuses)
	return p
}

// retryable reports whether err is worth another attempt.
func (p RetryPolicy) retryable(err error) bool {
	var builtin bool
	var timeout *TimeoutError
	var connection *ConnectionError
	var api *APIError
	switch {
	case errors.As(err, &timeout):
		builtin = p.RetryTimeouts
	case errors.As(err, &connection):
		builtin = p.RetryConnectionErrors
	case errors.As(err, &api):
		builtin = slices.Contains(p.RetryStatuses, api.Status)
	}
	return builtin || (p.Retry != nil && p.Retry(err))
}

// delay returns how long to wait before the attempt that follows err.
// attempt is 1 for the wait after the first attempt.
func (p RetryPolicy) delay(attempt int, err error) time.Duration {
	if p.RespectRetryAfter {
		var api *APIError
		if errors.As(err, &api) {
			if wait, ok := api.RetryAfter(); ok {
				return wait
			}
		}
	}
	return backoff(attempt, p.BackoffInitial, p.BackoffMax, p.BackoffJitter)
}

// backoff computes the exponential delay for an attempt, less a random share of
// it so that concurrent callers do not retry in lockstep.
func backoff(attempt int, initial, maximum time.Duration, jitter float64) time.Duration {
	if initial <= 0 || maximum <= 0 {
		return 0
	}
	exponential := maximum
	if shift := attempt - 1; shift < 62 {
		if scaled := initial << shift; scaled > 0 && scaled < maximum {
			exponential = scaled
		}
	}
	delay := time.Duration(float64(exponential) * (1 - rand.Float64()*jitter))
	return min(exponential, delay)
}
