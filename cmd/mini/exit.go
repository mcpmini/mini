package main

import (
	"errors"
	"fmt"
	"strings"
)

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErrf(format string, a ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, a...)}
}

func exitCodeFor(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	// cobra's Find() has no typed error for an unrecognized subcommand; pinned by
	// TestExitCodeFor so a wording change here fails CI instead of silently
	// reverting to exit 1.
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return 2
	}
	return 1
}
