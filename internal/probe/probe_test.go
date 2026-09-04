package probe

import (
	"testing"
)

func TestLocalProbeRefusalIsEngineFailureCandidate(t *testing.T) {
	message := "Get https://example.com: proxyconnect tcp: dial tcp 127.0.0.1:19002: connect: connection refused"
	if !isLocalProxyFailure(message) {
		t.Fatal("local probe refusal was treated as a remote node failure")
	}
	if isLocalProxyFailure("dial tcp 203.0.113.1:443: connect: connection refused") {
		t.Fatal("remote refusal was treated as a local probe failure")
	}
}
