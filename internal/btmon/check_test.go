// ABOUTME: Tests of the btmon trace checker against the real btmon trace from
// ABOUTME: the lola timing capture, and altered copies of it.

package btmon

import (
	"os"
	"strings"
	"testing"
)

const controller = "C3:00:00:58:51:40"

// realTrace is btmon -r's decoding of the lola timing capture: five
// connections to the controller, 21 status requests among them.
func realTrace(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/lola-session-1.btmon.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestBtmonCheckerAcceptsRealLolaTrace(t *testing.T) {
	report, err := Check(strings.NewReader(realTrace(t)), controller)
	if err != nil {
		t.Fatalf("Check rejected the real trace: %v", err)
	}
	if report.Connections != 5 {
		t.Errorf("%d connections to the controller, want 5", report.Connections)
	}
	if report.StatusRequests != 21 {
		t.Errorf("%d status requests, want 21", report.StatusRequests)
	}
	// 21 to FFF2, 10 to FFF1's CCCD, 5 to the Service Changed CCCD, which
	// BlueZ writes itself.
	if len(report.Writes) != 36 {
		t.Errorf("%d writes, want 36", len(report.Writes))
	}
	m := report.Map
	if m.FFF2Value != 0x0011 || m.FFF1Value != 0x000e || m.FFF1CCCD != 0x000f {
		t.Errorf("handle map FFF2 %#04x, FFF1 %#04x, FFF1 CCCD %#04x, want 0x0011, 0x000e, 0x000f", m.FFF2Value, m.FFF1Value, m.FFF1CCCD)
	}
	if m.FFF0Start != 0x000c || m.FFF0End != 0xffff {
		t.Errorf("FFF0 range %#04x-%#04x, want 0x000c-0xffff", m.FFF0Start, m.FFF0End)
	}
}
