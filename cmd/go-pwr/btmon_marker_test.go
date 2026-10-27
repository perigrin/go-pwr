// ABOUTME: Tests the match for "the link to the controller came up" in btmon's text,
// ABOUTME: on the real lola trace and altered copies, since it needs no hardware.

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// linkUpMarker matches btmon's text for a successful LE connection to the
// controller at mac: the Connection Complete event with Status: Success and
// Peer address: mac inside the same packet, so another device's connection is
// not taken for it.
func linkUpMarker(mac string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)LE Connection Complete \(0x01\)\n(?:[ \t]+[^\n]*\n)*?[ \t]+Status: Success[^\n]*\n(?:[ \t]+[^\n]*\n)*?[ \t]+Peer address: ` + regexp.QuoteMeta(mac))
}

// linkUpBefore says whether text, cut off at byte limit, holds the link to the
// controller coming up. The limit is where btmon's text stood shortly after
// go-pwr exited, so a connection that completes after go-pwr is gone is not
// credited to its run.
func linkUpBefore(text, mac string, limit int) bool {
	if limit < len(text) {
		text = text[:limit]
	}
	return linkUpMarker(mac).MatchString(text)
}

const markerMAC = "C3:00:00:58:51:40"

func realBtmonText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/btmon/testdata/lola-session-1.btmon.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLinkUpMatchesOnlyTheControllersSuccessfulConnection(t *testing.T) {
	real := realBtmonText(t)
	whole := len(real)
	for _, tc := range []struct {
		name string
		text string
		mac  string
		want bool
	}{
		{"real trace, the controller", real, markerMAC, true},
		{"real trace, lower-case address", real, strings.ToLower(markerMAC), true},
		{"altered-other-controller-asked-for", real, "00:11:22:33:44:55", false},
		{"altered-peer-address", strings.ReplaceAll(real, "Peer address: C3:00:00:58:51:40", "Peer address: 11:22:33:44:55:66"), markerMAC, false},
		{"altered-connection-failed", strings.ReplaceAll(real, "LE Connection Complete (0x01)\n        Status: Success (0x00)", "LE Connection Complete (0x01)\n        Status: Connection Timeout (0x08)"), markerMAC, false},
		{"empty", "", markerMAC, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkUpBefore(tc.text, tc.mac, whole); got != tc.want {
				t.Errorf("linkUpBefore = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLinkUpOnlyCountsWhatWasPrintedBeforeTheLimit(t *testing.T) {
	real := realBtmonText(t)
	at := strings.Index(real, "LE Connection Complete (0x01)")
	if at < 0 {
		t.Fatal("no connection in the real trace")
	}
	end := at + strings.Index(real[at:], "Peer address: C3:00:00:58:51:40") + len("Peer address: C3:00:00:58:51:40")
	if linkUpBefore(real, markerMAC, at) {
		t.Error("a connection printed after the limit was credited")
	}
	if linkUpBefore(real, markerMAC, end-5) {
		t.Error("a connection cut off before its peer address was credited")
	}
	if !linkUpBefore(real, markerMAC, end+1) {
		t.Error("a connection printed before the limit was not credited")
	}
	if !linkUpBefore(real, markerMAC, len(real)+1000) {
		t.Error("a limit past the end of the text was not clamped")
	}
}
