//go:build integration

package main

import "github.com/mcpmini/mini/internal/transport"

//sumtype:decl
type dispatchResult interface{ dispatchResult() }

type (
	continueResult struct{}
	responseResult struct{ response transport.Response }
	rawWriteResult struct{ data []byte }
	exitResult     struct{}
)

func (continueResult) dispatchResult() {}
func (responseResult) dispatchResult() {}
func (rawWriteResult) dispatchResult() {}
func (exitResult) dispatchResult()     {}
