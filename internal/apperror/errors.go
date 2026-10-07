// Package apperror defines operational errors shared by use cases and adapters.
// It has no dependency on a transport or provider implementation.
package apperror

import "errors"

var (
	ErrBusy     = errors.New("upstream request capacity exhausted")
	ErrUpstream = errors.New("upstream provider failure")
)
