package claims

import "errors"

// PermanentError marks a failure that retrying cannot fix (a malformed payload, a request the
// worker rejects as invalid). The coalescer quarantines the activity at once instead of retrying.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap exposes the cause.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a permanent failure (nil stays nil).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// classified is implemented by errors that know whether a retry can help (workerclient.Error).
type classified interface{ Permanent() bool }

// IsPermanent reports whether err is, or wraps, a failure that no retry can fix. Everything else
// (network errors, timeouts, 5xx, database errors) is retryable.
func IsPermanent(err error) bool {
	var p *PermanentError
	if errors.As(err, &p) {
		return true
	}
	var c classified
	return errors.As(err, &c) && c.Permanent()
}

// providerUnavailable is implemented by errors that say the model provider cannot serve calls and no
// retry will help (credits, quota, authentication, or core's own open circuit breaker; HAR-135). Such an
// error is not Permanent: the activity is fine, the provider is not, so nothing is quarantined.
type providerUnavailable interface{ ProviderUnavailable() bool }

// providerFault is implemented by errors that count against the provider circuit breaker.
type providerFault interface{ ProviderFault() bool }

// IsProviderUnavailable reports whether err is, or wraps, a non-retryable provider refusal. The job is
// parked at once: retrying only stacks failed (and, on a funded key, billed) calls.
func IsProviderUnavailable(err error) bool {
	var p providerUnavailable
	return errors.As(err, &p) && p.ProviderUnavailable()
}

// IsProviderFault reports whether err should count toward the provider circuit breaker: a non-retryable
// provider refusal, an unreachable worker or a worker 5xx.
func IsProviderFault(err error) bool {
	if IsProviderUnavailable(err) {
		return true
	}
	var f providerFault
	return errors.As(err, &f) && f.ProviderFault()
}
