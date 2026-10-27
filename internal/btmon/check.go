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
	// WritesSeen counts the ATT writes the host sent on any connection,
	// whether the checker recognises it as the controller's or not. A trace
	// whose connection the checker cannot find still shows here that
	// something was written.
	WritesSeen int
}

// ErrNoConnection and ErrNoHandleMap are what Check returns for a trace that
// shows nothing to judge. The report still holds the writes it found, but
// without a handle map they cannot be told apart.
var (
	ErrNoConnection = errors.New("no connection to the controller in the trace")
	ErrNoHandleMap  = errors.New("no handle map from discovery in the trace")
)

// connKey names a connection: handles are per adapter, so two adapters can
// each have a connection 64.
type connKey struct {
	adapter string
	handle  uint16
}

// Check reads btmon's text output (btmon -r on a capture) and reports the
// writes made on connections to the controller at mac. It returns an error
// naming every write that go-pwr must never make, and also when the trace
// holds no connection to the controller or no handle map for it, since a
// trace that shows nothing proves nothing. Connections to other devices are
// not counted in Writes, but every write on any connection is counted in
// WritesSeen. The report is never nil.
func Check(trace io.Reader, mac string) (*Report, error) {
	report := &Report{}
	packets, err := splitPackets(trace)
	if err != nil {
		return report, err
	}
	mac = strings.ToUpper(mac)
	var disc discovery
	var problems []error
	connected := map[connKey]bool{} // connections now open, and whether to the controller
	var writes []write
	for _, p := range packets {
		if ev, ok := connectionEventOf(p); ok {
			key := connKey{p.adapter, ev.handle}
			if ev.connected {
				connected[key] = ev.address == mac
				if ev.address == mac {
					report.Connections++
				}
			} else {
				delete(connected, key)
			}
			continue
		}
		pdu, ok := attPDUOf(p)
		if !ok {
			continue
		}
		if pdu.tx && isWrite(pdu.opcode) {
			report.WritesSeen++
		}
		if !connected[connKey{p.adapter, pdu.conn}] {
			continue
		}
		switch {
		case !pdu.tx && pdu.opcode == opReadByGroupTypeResponse:
			disc.learnServices(pdu.fields)
		case !pdu.tx && pdu.opcode == opReadByTypeResponse:
			disc.learnCharacteristics(pdu.fields)
		case pdu.tx && pdu.unparsed:
			problems = append(problems, fmt.Errorf("line %d: ATT PDU with no opcode, which go-pwr never sends", pdu.line))
		case pdu.tx && isWrite(pdu.opcode):
			writes = append(writes, writeOf(pdu))
		case pdu.tx && !isKnownNonWrite(pdu.opcode):
			problems = append(problems, fmt.Errorf("line %d: ATT opcode %#02x is not one the checker knows to be a non-write, so it might be a write", pdu.line, pdu.opcode))
		}
	}

	for _, w := range writes {
		report.Writes = append(report.Writes, Write{Line: w.line, Conn: w.conn, Opcode: w.opcode, Handle: w.handle, Data: w.data})
	}
	if report.Connections == 0 {
		return report, fmt.Errorf("%w: %s", ErrNoConnection, mac)
	}
	var discoveryProblems []error
	report.Map, discoveryProblems = disc.build()
	if !report.Map.complete() {
		return report, errors.Join(append([]error{fmt.Errorf("%w for %s: discovery named FFF0 at %#04x, FFF1 at %#04x with CCCD %#04x, FFF2 at %#04x", ErrNoHandleMap, mac, report.Map.FFF0Start, report.Map.FFF1Value, report.Map.FFF1CCCD, report.Map.FFF2Value)}, discoveryProblems...)...)
	}
	problems = append(problems, discoveryProblems...)
	for _, w := range writes {
		if err := report.Map.judge(w); err != nil {
			problems = append(problems, fmt.Errorf("line %d: %w", w.line, err))
		} else if w.handle == report.Map.FFF2Value {
			report.StatusRequests++
		}
	}
	return report, errors.Join(problems...)
}

// service is a primary service from a Read By Group Type Response; uuid is
// empty for one that is not a 16-bit UUID.
type service struct {
	start, end uint16
	uuid       string
}

// characteristic is a declaration from a Read By Type Response.
type characteristic struct {
	decl, value uint16
	props       byte
	uuid        string
}

// discovery is everything the controller's discovery responses said, kept
// until the end of the trace so that it can be judged whole.
type discovery struct {
	services []service
	chars    []characteristic
}

// learnServices reads a Read By Group Type Response: pairs of a handle range
// and the service's UUID.
func (d *discovery) learnServices(fields []string) {
	var cur *service
	for _, f := range fields {
		if r := handleRange.FindStringSubmatch(f); r != nil {
			cur = &service{start: hex16(r[1]), end: hex16(r[2])}
		}
		if strings.HasPrefix(f, "UUID:") && cur != nil {
			if u := uuid16.FindStringSubmatch(f); u != nil {
				cur.uuid = strings.ToLower(u[1])
			}
			d.services = append(d.services, *cur)
			cur = nil
		}
	}
}

// learnCharacteristics reads a Read By Type Response for characteristic
// declarations: each has a properties byte, a value handle and a value UUID.
func (d *discovery) learnCharacteristics(fields []string) {
	var cur *characteristic
	for _, f := range fields {
		if h := attHandle.FindStringSubmatch(f); h != nil {
			cur = &characteristic{decl: hex16(h[1])}
		}
		if cur == nil {
			continue
		}
		if p := properties.FindStringSubmatch(f); p != nil {
			cur.props = byte(hex16(p[1]))
		}
		if v := valueHandle.FindStringSubmatch(f); v != nil {
			cur.value = hex16(v[1])
		}
		if strings.HasPrefix(f, "Value UUID:") {
			if u := uuid16.FindStringSubmatch(f); u != nil {
				cur.uuid = strings.ToLower(u[1])
			}
			if cur.value != 0 {
				d.chars = append(d.chars, *cur)
			}
			cur = nil
		}
	}
}

// Characteristic property bits that make a characteristic carry a CCCD.
const notifyOrIndicate = 0x10 | 0x20

// build turns the discovery into the handle map, and says where it
// contradicts itself: a handle or UUID named twice with different meanings,
// FFF1 or FFF2 outside the FFF0 service, or a CCCD that cannot be told.
//
// Find Information named no descriptor on the real controller, so a CCCD is
// inferred, and only when it is the one handle between a notifying or
// indicating characteristic's value and the next declaration or the end of
// its service, which is where BlueZ wrote them. Two or more handles there
// would be a guess.
func (d *discovery) build() (HandleMap, []error) {
	m := HandleMap{otherCCCDs: map[uint16]bool{}}
	var problems []error
	conflict := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("discovery contradicts itself: "+format, args...))
	}

	services := map[uint16]service{}
	for _, s := range d.services {
		if old, ok := services[s.start]; ok && old != s {
			conflict("service at %#04x is %#04x-%#04x uuid %q and %#04x-%#04x uuid %q", s.start, old.start, old.end, old.uuid, s.start, s.end, s.uuid)
		}
		services[s.start] = s
		if s.uuid == "fff0" {
			if m.FFF0Start != 0 && (m.FFF0Start != s.start || m.FFF0End != s.end) {
				conflict("FFF0 is %#04x-%#04x and %#04x-%#04x", m.FFF0Start, m.FFF0End, s.start, s.end)
			}
			m.FFF0Start, m.FFF0End = s.start, s.end
		}
	}

	chars := map[uint16]characteristic{}
	byValue := map[uint16]characteristic{}
	for _, c := range d.chars {
		if old, ok := chars[c.decl]; ok && old != c {
			conflict("declaration %#04x names value %#04x uuid %q and value %#04x uuid %q", c.decl, old.value, old.uuid, c.value, c.uuid)
		}
		if old, ok := byValue[c.value]; ok && old.uuid != c.uuid {
			conflict("value handle %#04x is uuid %q and uuid %q", c.value, old.uuid, c.uuid)
		}
		chars[c.decl] = c
		byValue[c.value] = c
	}

	inFFF0 := func(c characteristic) bool {
		return m.FFF0Start != 0 && c.decl >= m.FFF0Start && c.value <= m.FFF0End
	}
	for _, c := range chars {
		switch c.uuid {
		case "fff1", "fff2":
			if !inFFF0(c) {
				conflict("%s at %#04x is not inside the FFF0 service %#04x-%#04x", strings.ToUpper(c.uuid), c.value, m.FFF0Start, m.FFF0End)
				continue
			}
			target := &m.FFF1Value
			if c.uuid == "fff2" {
				target = &m.FFF2Value
			}
			if *target != 0 && *target != c.value {
				conflict("%s is at %#04x and at %#04x", strings.ToUpper(c.uuid), *target, c.value)
			}
			*target = c.value
		case "ffe1":
			if !slices.Contains(m.FFE1Values, c.value) {
				m.FFE1Values = append(m.FFE1Values, c.value)
			}
		case "2b29":
			m.ClientFeatures = c.value
		}
	}

	for _, c := range chars {
		if c.props&notifyOrIndicate == 0 {
			continue
		}
		cccd, ok := d.soleHandleAfter(c, chars)
		if !ok {
			problems = append(problems, fmt.Errorf("cannot tell the CCCD of the %q characteristic with value %#04x: it is not the one handle before the next declaration or the end of its service", c.uuid, c.value))
			continue
		}
		if c.uuid == "fff1" && c.value == m.FFF1Value {
			m.FFF1CCCD = cccd
		} else {
			m.otherCCCDs[cccd] = true
		}
	}
	slices.Sort(m.FFE1Values)
	return m, problems
}

// soleHandleAfter says the handle after the characteristic's value, if it is
// the only one before the next declaration or the end of the service that
// holds the characteristic.
func (d *discovery) soleHandleAfter(c characteristic, chars map[uint16]characteristic) (uint16, bool) {
	bound := 0x10000 // exclusive
	for decl := range chars {
		if decl > c.value && int(decl) < bound {
			bound = int(decl)
		}
	}
	holder := false
	for _, s := range d.services {
		if s.start <= c.decl && c.decl <= s.end {
			holder = true
			if int(s.end)+1 < bound {
				bound = int(s.end) + 1
			}
		}
	}
	if !holder || bound != int(c.value)+2 {
		return 0, false
	}
	return c.value + 1, true
}

func hex16(s string) uint16 {
	n, _ := strconv.ParseUint(s, 16, 16)
	return uint16(n)
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
