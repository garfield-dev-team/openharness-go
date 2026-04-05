package types

import "fmt"

// OpenHarnessApiError is the base interface for upstream API failures.
type OpenHarnessApiError interface {
	error
	IsOpenHarnessApiError()
}

// AuthenticationFailure is raised when the upstream service rejects the provided credentials.
type AuthenticationFailure struct {
	Message string
}

func (e *AuthenticationFailure) Error() string          { return fmt.Sprintf("AuthenticationFailure: %s", e.Message) }
func (e *AuthenticationFailure) IsOpenHarnessApiError() {}

// RateLimitFailure is raised when the upstream service rejects the request due to rate limits.
type RateLimitFailure struct {
	Message string
}

func (e *RateLimitFailure) Error() string          { return fmt.Sprintf("RateLimitFailure: %s", e.Message) }
func (e *RateLimitFailure) IsOpenHarnessApiError() {}

// RequestFailure is raised for generic request or transport failures.
type RequestFailure struct {
	Message string
}

func (e *RequestFailure) Error() string          { return fmt.Sprintf("RequestFailure: %s", e.Message) }
func (e *RequestFailure) IsOpenHarnessApiError() {}

// IsOpenHarnessError checks whether an error implements OpenHarnessApiError.
func IsOpenHarnessError(err error) bool {
	_, ok := err.(OpenHarnessApiError)
	return ok
}
