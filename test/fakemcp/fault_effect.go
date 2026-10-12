//go:build integration

package main

import "time"

type registeredFault struct {
	spec   Fault
	effect faultEffect
}

//sumtype:decl
type faultEffect interface{ faultEffect() }

type (
	delayFault       struct{ duration time.Duration }
	slowInitFault    struct{ duration time.Duration }
	hangFault        struct{}
	dropFault        struct{}
	badJSONFault     struct{}
	rpcErrorFault    struct{ message string }
	errorResultFault struct{ message string }
	oversizedFault   struct{ sizeBytes int }
)

func (delayFault) faultEffect()       {}
func (slowInitFault) faultEffect()    {}
func (hangFault) faultEffect()        {}
func (dropFault) faultEffect()        {}
func (badJSONFault) faultEffect()     {}
func (rpcErrorFault) faultEffect()    {}
func (errorResultFault) faultEffect() {}
func (oversizedFault) faultEffect()   {}
