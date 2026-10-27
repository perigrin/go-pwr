// ABOUTME: Checks a btmon trace of go-pwr's connection to the controller: every
// ABOUTME: ATT write must be 0x20 to FFF2 or a CCCD write, judged by handle.

package btmon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// HandleMap is what the trace's own service discovery says about the
// controller's attribute handles. A zero handle is one it did not name.
type HandleMap struct {
	FFF0Start, FFF0End uint16 // the FFF0 service
	FFF1Value          uint16 // notifications of the reply
	FFF1CCCD           uint16 // inferred as the handle after FFF1Value
	FFF2Value          uint16 // the status request is written here
	ClientFeatures     uint16 // Client Supported Features, 0x2B29, if the device has one
	FFE1Values         []uint16
	otherCCCDs         map[uint16]bool // CCCDs of other notifying or indicating characteristics
}

func (m HandleMap) complete() bool {
	return m.FFF0Start != 0 && m.FFF1Value != 0 && m.FFF1CCCD != 0 && m.FFF2Value != 0
}

// Write is one ATT write on a connection to the controller.
type Write struct {
	Line   int // line of the packet in the trace
	Conn   uint16
	Opcode byte
	Handle uint16
	Data   []byte
}

// Report is what Check found in a trace.
type Report struct {
	Connections    int // connections to the controller
	Map            HandleMap
	Writes         []Write // every write on those connections
	StatusRequests int     // writes of 0x20 to FFF2
}

// Check reads btmon's text output (btmon -r on a capture) and reports the
// writes made on connections to the controller at mac. It returns an error
// naming every write that go-pwr must never make, and also when the trace
// holds no connection to the controller or no handle map for it, since a
// trace that shows nothing proves nothing. Connections to other devices are
// not counted.
func Check(trace io.Reader, mac string) (*Report, error) {
	packets, err := splitPackets(trace)
	if err != nil {
		return nil, err
	}
	mac = strings.ToUpper(mac)
	report := &Report{}
	report.Map.otherCCCDs = map[uint16]bool{}
	connected := map[uint16]bool{} // handles now connected to the controller
	var writes []write
	for _, p := range packets {
		if ev, ok := connectionEventOf(p); ok {
			if ev.connected {
				connected[ev.handle] = ev.address == mac
				if ev.address == mac {
					report.Connections++
				}
			} else {
				delete(connected, ev.handle)
			}
			continue
		}
		pdu, ok := attPDUOf(p)
		if !ok || !connected[pdu.conn] {
			continue
		}
		switch {
		case !pdu.tx && pdu.opcode == opReadByGroupTypeResponse:
			report.Map.learnServices(pdu.fields)
		case !pdu.tx && pdu.opcode == opReadByTypeResponse:
			report.Map.learnCharacteristics(pdu.fields)
		case pdu.tx && isWrite(pdu.opcode):
			writes = append(writes, writeOf(pdu))
		}
	}

	var problems []error
	if report.Connections == 0 {
		problems = append(problems, fmt.Errorf("no connection to %s in the trace", mac))
	}
	if !report.Map.complete() {
		problems = append(problems, fmt.Errorf("no handle map for %s in the trace: discovery named FFF0 at %#04x, FFF1 at %#04x, FFF2 at %#04x", mac, report.Map.FFF0Start, report.Map.FFF1Value, report.Map.FFF2Value))
	}
	if len(problems) > 0 {
		return report, errors.Join(problems...)
	}
	for _, w := range writes {
		report.Writes = append(report.Writes, Write{Line: w.line, Conn: w.conn, Opcode: w.opcode, Handle: w.handle, Data: w.data})
		if err := report.Map.judge(w); err != nil {
			problems = append(problems, fmt.Errorf("line %d: %w", w.line, err))
		} else if w.handle == report.Map.FFF2Value {
			report.StatusRequests++
		}
	}
	return report, errors.Join(problems...)
}

// learnServices reads a Read By Group Type Response: pairs of a handle range
// and the service's UUID.
func (m *HandleMap) learnServices(fields []string) {
	var start, end uint16
	for _, f := range fields {
		if r := handleRange.FindStringSubmatch(f); r != nil {
			start, end = hex16(r[1]), hex16(r[2])
		}
		if u := uuid16.FindStringSubmatch(f); u != nil && strings.EqualFold(u[1], "fff0") {
			m.FFF0Start, m.FFF0End = start, end
		}
	}
}

// Characteristic property bits that make a characteristic carry a CCCD.
const notifyOrIndicate = 0x10 | 0x20

// learnCharacteristics reads a Read By Type Response for characteristic
// declarations: each has a properties byte, a value handle and a value UUID.
// Find Information named no descriptor on the real controller, so a CCCD is
// taken to sit on the handle after the value of a notifying or indicating
// characteristic, which is where BlueZ wrote them.
func (m *HandleMap) learnCharacteristics(fields []string) {
	var props byte
	var value uint16
	for _, f := range fields {
		if attHandle.MatchString(f) {
			props, value = 0, 0
		}
		if p := properties.FindStringSubmatch(f); p != nil {
			props = byte(hex16(p[1]))
		}
		if v := valueHandle.FindStringSubmatch(f); v != nil {
			value = hex16(v[1])
		}
		if u := uuid16.FindStringSubmatch(f); u != nil && strings.HasPrefix(f, "Value UUID") && value != 0 {
			m.learn(strings.ToLower(u[1]), value, props)
		}
	}
}

// learn records a characteristic by its 16-bit UUID, value handle and properties.
func (m *HandleMap) learn(uuid string, value uint16, props byte) {
	switch uuid {
	case "fff1":
		m.FFF1Value = value
		m.FFF1CCCD = value + 1
	case "fff2":
		m.FFF2Value = value
	case "ffe1":
		m.FFE1Values = append(m.FFE1Values, value)
	case "2b29":
		m.ClientFeatures = value
	default:
		if props&notifyOrIndicate != 0 {
			m.otherCCCDs[value+1] = true
		}
	}
}

// judge says why a write is not one go-pwr may make, or nil if it is: 0x20
// to FFF2, a notifications on or off CCCD write to FFF1, or a CCCD write of
// any other service (BlueZ writes the Service Changed CCCD itself).
func (m HandleMap) judge(w write) error {
	if w.opcode != opWriteRequest && w.opcode != opWriteCommand {
		return fmt.Errorf("%s: go-pwr never makes this kind of write", opcodeName(w.opcode))
	}
	if !w.hasHandle {
		return fmt.Errorf("%s with no handle", opcodeName(w.opcode))
	}
	if w.dataErr != nil {
		return fmt.Errorf("write to %#04x: %w", w.handle, w.dataErr)
	}
	h := w.handle
	switch {
	case h == m.FFF2Value:
		if !bytes.Equal(w.data, []byte{0x20}) {
			return fmt.Errorf("write of % x to FFF2 (%#04x), want exactly the single byte 20", w.data, h)
		}
	case h == m.FFF1CCCD:
		if !bytes.Equal(w.data, []byte{0x01, 0x00}) && !bytes.Equal(w.data, []byte{0x00, 0x00}) {
			return fmt.Errorf("write of % x to FFF1's CCCD (%#04x), want 0100 (notifications on) or 0000 (off)", w.data, h)
		}
	case slices.Contains(m.FFE1Values, h):
		return fmt.Errorf("write of % x to FFE1 (%#04x)", w.data, h)
	case m.ClientFeatures != 0 && h == m.ClientFeatures:
		return fmt.Errorf("write of % x to Client Supported Features (0x2B29, %#04x)", w.data, h)
	case h >= m.FFF0Start && h <= m.FFF0End:
		return fmt.Errorf("write of % x to %#04x inside FFF0, which is neither FFF2 nor FFF1's CCCD", w.data, h)
	case m.otherCCCDs[h]:
		if !bytes.Equal(w.data, []byte{0x00, 0x00}) && !bytes.Equal(w.data, []byte{0x01, 0x00}) && !bytes.Equal(w.data, []byte{0x02, 0x00}) {
			return fmt.Errorf("write of % x to the CCCD at %#04x, want 0000, 0100 or 0200", w.data, h)
		}
	default:
		return fmt.Errorf("write of % x to %#04x, outside FFF0 and not a CCCD", w.data, h)
	}
	return nil
}

func opcodeName(op byte) string {
	switch op {
	case opWriteRequest:
		return "Write Request (0x12)"
	case opWriteCommand:
		return "Write Command (0x52)"
	case opSignedWriteCommand:
		return "Signed Write Command (0xd2)"
	case opPrepareWriteRequest:
		return "Prepare Write Request (0x16)"
	case opExecuteWriteRequest:
		return "Execute Write Request (0x18)"
	}
	return fmt.Sprintf("ATT opcode %#02x", op)
}

func hex16(s string) uint16 {
	n, _ := strconv.ParseUint(s, 16, 16)
	return uint16(n)
}
