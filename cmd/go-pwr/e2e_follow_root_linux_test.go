// ABOUTME: End-to-end tests of --follow on lola, run as root, for startup
// ABOUTME: failures and for failures in the middle of a run that it survives.

package main

import "testing"

// followThroughFailure runs throughFailure with --follow.
func followThroughFailure(t *testing.T, mac string, breakIt, fix func(*testing.T)) {
	t.Helper()
	throughFailure(t, mac, "--follow=8s", breakIt, fix)
}

func TestFollowContinuesAfterAdapterUnbound(t *testing.T) {
	mac := requireRoot(t)
	followThroughFailure(t, mac, unbindAdapter, rebindAdapter)
}
