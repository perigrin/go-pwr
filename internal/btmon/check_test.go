// ABOUTME: Tests of the btmon trace checker against the real btmon trace from
// ABOUTME: the lola timing capture, and altered copies of it.

package btmon

import (
	"errors"
	"fmt"
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

// writeAt finds the first Write Request to handle in a trace and returns the
// index of its ATT line. The handle and data lines follow it.
func writeAt(t *testing.T, lines []string, handle string) int {
	t.Helper()
	for i := 0; i+2 < len(lines); i++ {
		if strings.Contains(lines[i], "ATT: Write Request (0x12)") && strings.Contains(lines[i+1], "Handle: "+handle) {
			return i
		}
	}
	t.Fatalf("no Write Request to %s in the trace", handle)
	return 0
}

// alter returns the real trace with one write rewritten by edit, which is
// given the ATT line, handle line and data line of the first write to handle.
func alter(t *testing.T, handle string, edit func(att, h, data string) []string) string {
	t.Helper()
	lines := strings.Split(realTrace(t), "\n")
	i := writeAt(t, lines, handle)
	var out []string
	out = append(out, lines[:i]...)
	out = append(out, edit(lines[i], lines[i+1], lines[i+2])...)
	out = append(out, lines[i+3:]...)
	return strings.Join(out, "\n")
}

// withCharacteristic adds a characteristic declaration to the first Read By
// Type Response that names FFF2, as if the controller had one more.
func withCharacteristic(t *testing.T, trace, valueHandle, uuid string) string {
	t.Helper()
	lines := strings.Split(trace, "\n")
	for i, line := range lines {
		if strings.Contains(line, "Value UUID: Aliro (0xfff2)") {
			extra := []string{
				"        Handle: 0x0013",
				"        Value[5]: 0c" + valueHandle[2:4] + "00" + "00" + "00",
				"            Properties: 0x0c",
				"              Write Without Response (0x04)",
				"              Write (0x08)",
				"            Value Handle: 0x" + valueHandle,
				"            Value UUID: Unknown (0x" + uuid + ")",
			}
			out := append([]string{}, lines[:i+1]...)
			out = append(out, extra...)
			return strings.Join(append(out, lines[i+1:]...), "\n")
		}
	}
	t.Fatal("no FFF2 characteristic in the trace")
	return ""
}

func TestBtmonCheckerRejectsAlteredTraces(t *testing.T) {
	const fff2 = "0x0011"
	setData := func(data string) func(att, h, d string) []string {
		return func(att, h, _ string) []string { return []string{att, h, data} }
	}
	for _, tc := range []struct {
		name  string
		trace string
		want  string
	}{
		{"altered-fff2-0x21", alter(t, fff2, setData("          Data[1]: 21")), "want exactly the single byte 20"},
		{"altered-fff2-two-bytes", alter(t, fff2, setData("          Data[2]: 2000")), "want exactly the single byte 20"},
		{"altered-fff2-empty", alter(t, fff2, setData("          Data[0]: ")), "want exactly the single byte 20"},
		{"altered-ffe1-write", func() string {
			trace := withCharacteristic(t, realTrace(t), "0014", "ffe1")
			lines := strings.Split(trace, "\n")
			i := writeAt(t, lines, fff2)
			lines[i+1] = "        Handle: 0x0014 Type: Unknown (0xffe1)"
			return strings.Join(lines, "\n")
		}(), "to FFE1"},
		{"altered-client-supported-features", func() string {
			trace := withCharacteristic(t, realTrace(t), "0016", "2b29")
			lines := strings.Split(trace, "\n")
			i := writeAt(t, lines, fff2)
			lines[i+1] = "        Handle: 0x0016 Type: Client Supported Features (0x2b29)"
			lines[i+2] = "          Data[1]: 01"
			return strings.Join(lines, "\n")
		}(), "Client Supported Features"},
		{"altered-prepare-write", alter(t, fff2, func(att, h, d string) []string {
			return []string{"      ATT: Prepare Write Request (0x16) len 5", h, "        Offset: 0x0000", d}
		}), "Prepare Write Request (0x16)"},
		{"altered-execute-write", alter(t, fff2, func(att, h, d string) []string {
			return []string{"      ATT: Execute Write Request (0x18) len 1", "        Flags: Write all (0x01)"}
		}), "Execute Write Request (0x18)"},
		{"altered-signed-write", alter(t, fff2, func(att, h, d string) []string {
			return []string{"      ATT: Signed Write Command (0xd2) len 3", h, d, "        Signature: 00000000000000000000000000"}
		}), "Signed Write Command (0xd2)"},
		{"altered-write-command-0x21", alter(t, fff2, func(att, h, d string) []string {
			return []string{"      ATT: Write Command (0x52) len 3", h, "          Data[1]: 21"}
		}), "want exactly the single byte 20"},
		{"altered-write-elsewhere-in-fff0", alter(t, fff2, func(att, h, d string) []string {
			return []string{att, "        Handle: 0x0012", d}
		}), "inside FFF0"},
		{"altered-write-outside-fff0", alter(t, fff2, func(att, h, d string) []string {
			return []string{att, "        Handle: 0x0003 Type: Device Name (0x2a00)", "          Data[1]: 41"}
		}), "outside FFF0 and not a CCCD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Check(strings.NewReader(tc.trace), controller)
			if err == nil {
				t.Fatal("Check passed the altered trace")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

func TestBtmonCheckerRejectsOtherCCCDValues(t *testing.T) {
	const fff1CCCD = "0x000f"
	for _, value := range []string{"0200", "0300", "0002", "0003", "01", "010000"} {
		t.Run("altered-fff1-cccd-"+value, func(t *testing.T) {
			trace := alter(t, fff1CCCD, func(att, h, d string) []string {
				return []string{att, h, fmt.Sprintf("          Data[%d]: %s", len(value)/2, value)}
			})
			_, err := Check(strings.NewReader(trace), controller)
			if err == nil || !strings.Contains(err.Error(), "FFF1's CCCD") {
				t.Errorf("Check = %v, want an error about FFF1's CCCD", err)
			}
		})
	}
}

// connectionTo is a btmon LE Connection Complete event.
func connectionTo(handle int, address string) []string {
	return []string{
		"> HCI Event: LE Meta Event (0x3e) plen 19                 #1 [hci0] 1.000000",
		"      LE Connection Complete (0x01)",
		"        Status: Success (0x00)",
		fmt.Sprintf("        Handle: %d", handle),
		"        Role: Central (0x00)",
		"        Peer address type: Public (0x00)",
		"        Peer address: " + address + " (OUI 11-22-33)",
	}
}

func TestBtmonCheckerScopesToControllerConnection(t *testing.T) {
	// Move the first FFF2 write, with a bad value, to a connection to some
	// other device: it is not counted. On the controller's own it fails.
	lines := strings.Split(realTrace(t), "\n")
	i := writeAt(t, lines, "0x0011")
	header := i - 1
	if !strings.Contains(lines[header], "ACL Data TX: Handle 64") {
		t.Fatalf("expected the ACL header above the write, got %q", lines[header])
	}
	badValue := func(handle string) string {
		altered := append([]string{}, lines...)
		altered[header] = strings.Replace(altered[header], "Handle 64", "Handle "+handle, 1)
		altered[i+2] = "          Data[1]: 21"
		if handle != "64" {
			altered = append(altered[:header:header], append(connectionTo(65, "11:22:33:44:55:66"), altered[header:]...)...)
		}
		return strings.Join(altered, "\n")
	}

	report, err := Check(strings.NewReader(badValue("65")), controller)
	if err != nil {
		t.Fatalf("a bad write on another device's connection failed the check: %v", err)
	}
	if report.StatusRequests != 20 || report.Connections != 5 {
		t.Errorf("%d status requests and %d connections, want 20 and 5: the write on the other connection is not the controller's", report.StatusRequests, report.Connections)
	}

	if _, err := Check(strings.NewReader(badValue("64")), controller); err == nil {
		t.Error("the same write on the controller's connection passed")
	}
}

func TestBtmonCheckerFailsClosed(t *testing.T) {
	// dropPackets removes every packet whose text contains substr.
	dropPackets := func(trace, substr string) string {
		var out, block []string
		flush := func() {
			if !strings.Contains(strings.Join(block, "\n"), substr) {
				out = append(out, block...)
			}
			block = nil
		}
		for _, line := range strings.Split(trace, "\n") {
			if line != "" && line[0] != ' ' {
				flush()
			}
			block = append(block, line)
		}
		flush()
		return strings.Join(out, "\n")
	}

	for _, tc := range []struct {
		name  string
		trace string
		mac   string
		want  error
	}{
		{"empty trace", "", controller, ErrNoConnection},
		{"trace of no ATT at all", "Bluetooth monitor ver 5.82\n= Note: Linux version 6.18.50\n", controller, ErrNoConnection},
		{"another controller's address", realTrace(t), "00:11:22:33:44:55", ErrNoConnection},
		{"no Connection Complete", dropPackets(realTrace(t), "LE Connection Complete"), controller, ErrNoConnection},
		{"no discovery", dropPackets(dropPackets(realTrace(t), "Read By Type Response"), "Read By Group Type Response"), controller, ErrNoHandleMap},
		{"no characteristic discovery", dropPackets(realTrace(t), "Read By Type Response"), controller, ErrNoHandleMap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Check(strings.NewReader(tc.trace), tc.mac)
			if !errors.Is(err, tc.want) {
				t.Errorf("Check = %v, want %v", err, tc.want)
			}
		})
	}
}
