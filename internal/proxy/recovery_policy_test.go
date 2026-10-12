package proxy

import "testing"

func TestRecoveryDeclinesNonRetryableOutcomes(t *testing.T) {
	for _, kind := range []outcomeKind{outcomeOK, outcomeOther, 99} {
		session := &proxySession{}
		state := linkState{generation: 7, token: "placeholder"}
		next, retry := session.recoverDaemonLink(state, kind)
		if retry || next != state {
			t.Errorf("outcome %d recovery = (%+v, %v), want unchanged state and no retry", kind, next, retry)
		}
	}
}
