// ABOUTME: Tests of the btmon trace checker against the real btmon trace from
// ABOUTME: the lola timing capture, and altered copies of it.

package btmon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/iotest"
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

// beforeFirstWrite returns the trace with extra lines inserted above the ACL
// header of the first Write Request to handle, on the controller's live
// connection.
func beforeFirstWrite(t *testing.T, trace, handle string, extra []string) string {
	t.Helper()
	lines := strings.Split(trace, "\n")
	i := writeAt(t, lines, handle) - 1
	out := append([]string{}, lines[:i]...)
	out = append(out, extra...)
	return strings.Join(append(out, lines[i:]...), "\n")
}

// characteristicResponse is a Read By Type Response on the controller's
// connection naming one characteristic.
func characteristicResponse(decl, value, uuid string) []string {
	return []string{
		"> ACL Data RX: Handle 64 flags 0x02 dlen 27               #9999 [hci0] 999.000000",
		"      ATT: Read By Type Response (0x09) len 8",
		"        Attribute data length: 7",
		"        Attribute data list: 1 entries",
		"        Handle: 0x" + decl,
		"        Value[5]: 0c0000f2ff",
		"            Properties: 0x0c",
		"              Write Without Response (0x04)",
		"              Write (0x08)",
		"            Value Handle: 0x" + value,
		"            Value UUID: Aliro (0x" + uuid + ")",
	}
}

func TestBtmonCheckerCountsWritesOnUnrecognisedConnections(t *testing.T) {
	// 21 status requests changed to 0x21, and the controller's connection
	// hidden from the checker, as if btmon had started after it. Every
	// write is on a connection the checker does not recognise.
	bad := strings.ReplaceAll(realTrace(t), "Data[1]: 20", "Data[1]: 21")
	hidden := strings.ReplaceAll(bad, "LE Connection Complete (0x01)", "LE Something Else (0x01)")
	report, err := Check(strings.NewReader(hidden), controller)
	if !errors.Is(err, ErrNoConnection) {
		t.Fatalf("Check = %v, want ErrNoConnection", err)
	}
	if len(report.Writes) != 0 {
		t.Errorf("%d writes attributed to the controller, want none", len(report.Writes))
	}
	if report.WritesSeen < 21 {
		t.Errorf("%d writes seen on any connection, want at least the 21 altered ones", report.WritesSeen)
	}
}

func TestBtmonCheckReturnsAReportOnReadError(t *testing.T) {
	report, err := Check(iotest.ErrReader(errors.New("boom")), controller)
	if err == nil || report == nil {
		t.Errorf("Check = %v, %v, want an error and a report that is not nil", report, err)
	}
}

func TestBtmonCheckerRejectsOtherCCCDValuesOnServiceChanged(t *testing.T) {
	const serviceChangedCCCD = "0x000b"
	for _, value := range []string{"0300", "0003", "01"} {
		t.Run("altered-service-changed-cccd-"+value, func(t *testing.T) {
			trace := alter(t, serviceChangedCCCD, func(att, h, d string) []string {
				return []string{att, h, fmt.Sprintf("          Data[%d]: %s", len(value)/2, value)}
			})
			_, err := Check(strings.NewReader(trace), controller)
			if err == nil || !strings.Contains(err.Error(), "want 0000, 0100 or 0200") {
				t.Errorf("Check = %v, want an error about the CCCD value", err)
			}
		})
	}
}

func TestBtmonCheckerKeysConnectionsByAdapter(t *testing.T) {
	// A Disconnect Complete for handle 64 on hci1 must not end the
	// controller's connection 64 on hci0: the bad write after it still counts.
	bad := alter(t, "0x0011", func(att, h, _ string) []string {
		return []string{att, h, "          Data[1]: 21"}
	})
	other := []string{
		"> HCI Event: Disconnect Complete (0x05) plen 4            #1 [hci1] 1.000000",
		"        Status: Success (0x00)",
		"        Handle: 64 Address: 11:22:33:44:55:66 (OUI 11-22-33)",
		"        Reason: Remote User Terminated Connection (0x13)",
	}
	_, err := Check(strings.NewReader(beforeFirstWrite(t, bad, "0x0011", other)), controller)
	if err == nil || !strings.Contains(err.Error(), "want exactly the single byte 20") {
		t.Errorf("Check = %v, want the bad write on hci0 still caught", err)
	}

	// And a connection on hci1 with the same handle is not the controller's.
	elsewhere := strings.Replace(realTrace(t), "[hci0]", "[hci1]", -1)
	if _, err := Check(strings.NewReader(elsewhere), "00:11:22:33:44:55"); !errors.Is(err, ErrNoConnection) {
		t.Errorf("Check = %v, want ErrNoConnection for another address on any adapter", err)
	}
}

func TestBtmonCheckerRejectsConflictingDiscovery(t *testing.T) {
	// A second FFF2 outside FFF0 (here at 0x0003, Device Name) would make
	// 0x20 to it look like a status request.
	moved := alter(t, "0x0011", func(att, _, d string) []string {
		return []string{att, "        Handle: 0x0003 Type: Device Name (0x2a00)", d}
	})
	for name, trace := range map[string]string{
		"altered-second-fff2-outside-fff0": beforeFirstWrite(t, moved, "0x0003", characteristicResponse("0002", "0003", "fff2")),
		"altered-second-fff2-inside-fff0":  beforeFirstWrite(t, realTrace(t), "0x0011", characteristicResponse("0012", "0013", "fff2")),
		"altered-second-fff1":              beforeFirstWrite(t, realTrace(t), "0x0011", characteristicResponse("0012", "0013", "fff1")),
		"altered-fff2-value-is-fff1-value": beforeFirstWrite(t, realTrace(t), "0x0011", characteristicResponse("0020", "000e", "fff2")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Check(strings.NewReader(trace), controller)
			if err == nil || !strings.Contains(err.Error(), "discovery") {
				t.Errorf("Check = %v, want an error about the discovery", err)
			}
		})
	}

	// FFF2 announced outside FFF0's range, with no other FFF2.
	outside := strings.Replace(realTrace(t), "Handle range: 0x000c-0xffff", "Handle range: 0x000c-0x000f", -1)
	if _, err := Check(strings.NewReader(outside), controller); err == nil {
		t.Error("an FFF2 outside the FFF0 range passed")
	}
}

func TestBtmonCheckerNeedsSoleHandleForCCCD(t *testing.T) {
	// Move FFF2's declaration and value up one: two handles, 0x000f and
	// 0x0010, now sit between FFF1's value and the next declaration, so
	// which one is the CCCD is a guess.
	gap := realTrace(t)
	gap = strings.Replace(gap, "Handle: 0x0010\n        Value[5]: 0c1100f2ff", "Handle: 0x0011\n        Value[5]: 0c1200f2ff", -1)
	gap = strings.Replace(gap, "Value Handle: 0x0011\n            Value UUID: Aliro", "Value Handle: 0x0012\n            Value UUID: Aliro", -1)
	if gap == realTrace(t) {
		t.Fatal("the trace was not altered")
	}
	_, err := Check(strings.NewReader(gap), controller)
	if err == nil || !strings.Contains(err.Error(), "cannot tell") {
		t.Errorf("Check = %v, want an error that the CCCD cannot be told", err)
	}
}

func TestBtmonCheckerRejectsUnknownOpcodes(t *testing.T) {
	for _, line := range []string{
		"      ATT: Unknown (0x92) len 3",
		"      ATT: Unknown len 3",
	} {
		t.Run(strings.TrimSpace(line), func(t *testing.T) {
			trace := alter(t, "0x0011", func(att, h, d string) []string { return []string{line, h, d} })
			_, err := Check(strings.NewReader(trace), controller)
			if err == nil || !strings.Contains(err.Error(), "ATT") {
				t.Errorf("Check = %v, want an error about the ATT opcode", err)
			}
		})
	}
}

func TestBtmonCheckerCountsUnknownOpcodesInWritesSeen(t *testing.T) {
	// An opcode the checker cannot class might be a write, so on a
	// connection it does not recognise it counts as one.
	for _, att := range []string{"      ATT: Unknown (0x92) len 3", "      ATT: Unknown len 3", "      ATT: Write Command (0x52) len 3"} {
		trace := "< ACL Data TX: Handle 99 flags 0x00 dlen 8                #1 [hci0] 1.000000\n" + att + "\n        Handle: 0x0011\n          Data[1]: 20\n"
		report, err := Check(strings.NewReader(trace), controller)
		if !errors.Is(err, ErrNoConnection) || report.WritesSeen != 1 {
			t.Errorf("%q: Check = %v with %d writes seen, want ErrNoConnection and 1", att, err, report.WritesSeen)
		}
	}
	// A known non-write does not count.
	trace := "< ACL Data TX: Handle 99 flags 0x00 dlen 8                #1 [hci0] 1.000000\n      ATT: Read Request (0x0a) len 2\n        Handle: 0x0003\n"
	if report, _ := Check(strings.NewReader(trace), controller); report.WritesSeen != 0 {
		t.Errorf("a Read Request counted as a write: %d", report.WritesSeen)
	}
}

func TestBtmonCheckerReportsEachLinkOfTheRealTrace(t *testing.T) {
	report, err := Check(strings.NewReader(realTrace(t)), controller)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Links) != 5 {
		t.Fatalf("%d links, want 5", len(report.Links))
	}
	// Every link in the capture has handle 64, so a write is told to its
	// link by the connection event before it, not by its handle.
	for i, l := range report.Links {
		if l.Handle != 64 || l.Up == 0 || l.Down <= l.Up {
			t.Errorf("link %d is %+v, want handle 64, brought up before it went down", i, l)
		}
		if i > 0 && l.Up <= report.Links[i-1].Down {
			t.Errorf("link %d came up at line %d, before link %d went down at line %d", i, l.Up, i-1, report.Links[i-1].Down)
		}
	}
	used := map[int]bool{}
	for _, w := range report.Writes {
		if w.Link < 0 || w.Link >= len(report.Links) {
			t.Fatalf("write at line %d has link %d of %d", w.Line, w.Link, len(report.Links))
		}
		l := report.Links[w.Link]
		if w.Line < l.Up || w.Line > l.Down {
			t.Errorf("write at line %d is on link %+v, which was not up then", w.Line, l)
		}
		used[w.Link] = true
	}
	if len(used) != 5 {
		t.Errorf("writes on %d links, want all 5", len(used))
	}
}

// withLeftoverLink returns the real trace with a link to the controller
// ahead of its first one, as `bluetoothctl connect` makes before a run. The
// extra link is dropped before the first real one comes up only if dropped.
func withLeftoverLink(t *testing.T, dropped bool) string {
	t.Helper()
	lines := strings.Split(realTrace(t), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "      LE Connection Complete") {
			extra := connectionTo(63, controller)
			if dropped {
				extra = append(extra,
					"> HCI Event: Disconnect Complete (0x05) plen 4            #1 [hci0] 1.000000",
					"        Status: Success (0x00)",
					"        Handle: 63 Address: "+controller+" (OUI C3-00-00)",
					"        Reason: Connection Terminated By Local Host (0x16)")
			}
			out := append([]string{}, lines[:i-1]...)
			out = append(out, extra...)
			return strings.Join(append(out, lines[i-1:]...), "\n")
		}
	}
	t.Fatal("no connection in the trace")
	return ""
}

func TestBtmonFirstRequestOnNewLink(t *testing.T) {
	check := func(trace string) error {
		report, err := Check(strings.NewReader(trace), controller)
		if err != nil {
			t.Fatal(err)
		}
		return report.FirstRequestOnNewLink()
	}

	if err := check(withLeftoverLink(t, true)); err != nil {
		t.Errorf("a leftover link dropped before the request's link came up: %v", err)
	}
	if err := check(withLeftoverLink(t, false)); err == nil {
		t.Error("a leftover link that was never dropped passed")
	}
	if err := check(realTrace(t)); err == nil {
		t.Error("a trace with no link before the request's passed")
	}
}

func TestBtmonFirstRequestOnNewLinkNeedsARequest(t *testing.T) {
	var report Report
	if err := report.FirstRequestOnNewLink(); err == nil {
		t.Error("an empty report passed")
	}
}
