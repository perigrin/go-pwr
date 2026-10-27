// ABOUTME: Parses btmon's text decoding of an HCI trace into packets, and the
// ABOUTME: packets into the connection events and ATT PDUs that the checker reads.

package btmon

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// packet is one header line of btmon's output and the indented lines below it.
type packet struct {
	line    int    // line number of the header
	adapter string // the [hciN] in the header, if it has one
	header  string
	body    []string
}

// splitPackets reads btmon's text output: a header starts in column 0, and
// its fields are indented below it.
func splitPackets(r io.Reader) ([]packet, error) {
	var packets []packet
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan(); n++ {
		text := scanner.Text()
		switch {
		case strings.TrimSpace(text) == "":
		case text[0] == ' ' || text[0] == '\t':
			if len(packets) > 0 {
				last := &packets[len(packets)-1]
				last.body = append(last.body, text)
			}
		default:
			packets = append(packets, packet{line: n, header: text, adapter: adapterRE.FindString(text)})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read btmon trace: %w", err)
	}
	return packets, nil
}

var (
	adapterRE     = regexp.MustCompile(`\[hci\d+\]`)
	aclHeader     = regexp.MustCompile(`^([<>]) ACL Data (TX|RX): Handle (\d+) `)
	attPrefix     = "ATT:"
	attLine       = regexp.MustCompile(`^ATT: .*\(0x([0-9a-fA-F]{2})\)`)
	hciHandleLine = regexp.MustCompile(`^Handle: (\d+)`)
	peerLine      = regexp.MustCompile(`^Peer address: ([0-9A-Fa-f:]{17})`)
	attHandle     = regexp.MustCompile(`^Handle: 0x([0-9a-fA-F]{4})`)
	handleRange   = regexp.MustCompile(`^Handle range: 0x([0-9a-fA-F]{4})-0x([0-9a-fA-F]{4})`)
	valueHandle   = regexp.MustCompile(`^Value Handle: 0x([0-9a-fA-F]{4})`)
	properties    = regexp.MustCompile(`^Properties: 0x([0-9a-fA-F]{2})`)
	uuid16        = regexp.MustCompile(`^(?:Value )?UUID: .*\(0x([0-9a-fA-F]{4})\)\s*$`)
	dataLine      = regexp.MustCompile(`^Data\[(\d+)\]: ([0-9a-fA-F]*)\s*$`)
)

// ATT opcodes the checker reads.
const (
	opReadByTypeResponse      = 0x09
	opReadByGroupTypeResponse = 0x11
	opWriteRequest            = 0x12
	opPrepareWriteRequest     = 0x16
	opExecuteWriteRequest     = 0x18
	opWriteCommand            = 0x52
	opSignedWriteCommand      = 0xd2
)

// isWrite reports whether the opcode is an ATT write of any kind.
func isWrite(op byte) bool {
	switch op {
	case opWriteRequest, opWriteCommand, opSignedWriteCommand, opPrepareWriteRequest, opExecuteWriteRequest:
		return true
	}
	return false
}

// isKnownNonWrite reports whether the opcode is an ATT opcode that is
// certainly not a write: the other requests and responses, notifications,
// indications and confirmations. An opcode in neither list is not trusted.
func isKnownNonWrite(op byte) bool {
	switch {
	case op >= 0x01 && op <= 0x11, op == 0x13, op == 0x17, op == 0x19,
		op == 0x1b, op == 0x1d, op == 0x1e, op == 0x20, op == 0x21, op == 0x23:
		return true
	}
	return false
}

// connectionEvent is an HCI LE connection or disconnection.
type connectionEvent struct {
	handle    uint16
	address   string // empty on a disconnection
	connected bool
}

// connectionEventOf reads a connection or disconnection event, if the packet
// is a successful one.
func connectionEventOf(p packet) (connectionEvent, bool) {
	if len(p.body) == 0 || !strings.HasPrefix(p.header, "> HCI Event:") {
		return connectionEvent{}, false
	}
	first := strings.TrimSpace(p.body[0])
	var ev connectionEvent
	switch {
	case strings.HasPrefix(first, "LE Connection Complete"), strings.HasPrefix(first, "LE Enhanced Connection Complete"):
		ev.connected = true
	case strings.HasPrefix(p.header, "> HCI Event: Disconnect Complete"):
	default:
		return connectionEvent{}, false
	}
	success, haveHandle := false, false
	for _, line := range p.body {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status: Success") {
			success = true
		}
		if m := hciHandleLine.FindStringSubmatch(line); m != nil && !haveHandle {
			n, _ := strconv.ParseUint(m[1], 10, 16)
			ev.handle, haveHandle = uint16(n), true
		}
		if m := peerLine.FindStringSubmatch(line); m != nil {
			ev.address = strings.ToUpper(m[1])
		}
	}
	if !success || !haveHandle || (ev.connected && ev.address == "") {
		return connectionEvent{}, false
	}
	return ev, true
}

// attPDU is an ATT PDU in an ACL packet.
type attPDU struct {
	line   int // line number of the ACL header
	tx     bool
	conn   uint16
	opcode byte
	// unparsed is an ATT line with no opcode in it, which cannot be classed.
	unparsed bool
	fields   []string // the trimmed lines below the ATT line
}

// attPDUOf reads the ATT PDU in an ACL packet, if it holds one.
func attPDUOf(p packet) (attPDU, bool) {
	h := aclHeader.FindStringSubmatch(p.header)
	if h == nil {
		return attPDU{}, false
	}
	conn, _ := strconv.ParseUint(h[3], 10, 16)
	for i, line := range p.body {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, attPrefix) {
			continue
		}
		pdu := attPDU{line: p.line, tx: h[2] == "TX", conn: uint16(conn)}
		if m := attLine.FindStringSubmatch(line); m != nil {
			op, _ := strconv.ParseUint(m[1], 16, 8)
			pdu.opcode = byte(op)
		} else {
			pdu.unparsed = true
		}
		for _, f := range p.body[i+1:] {
			pdu.fields = append(pdu.fields, strings.TrimSpace(f))
		}
		return pdu, true
	}
	return attPDU{}, false
}

// write is an ATT write that went out on a connection to the controller.
type write struct {
	line      int
	conn      uint16
	opcode    byte
	hasHandle bool
	handle    uint16
	data      []byte
	dataErr   error
}

// writeOf reads a write PDU's handle and data.
func writeOf(p attPDU) write {
	w := write{line: p.line, conn: p.conn, opcode: p.opcode}
	for _, f := range p.fields {
		if m := attHandle.FindStringSubmatch(f); m != nil && !w.hasHandle {
			n, _ := strconv.ParseUint(m[1], 16, 16)
			w.handle, w.hasHandle = uint16(n), true
		}
		if m := dataLine.FindStringSubmatch(f); m != nil {
			want, _ := strconv.Atoi(m[1])
			data, err := hex.DecodeString(m[2])
			switch {
			case err != nil:
				w.dataErr = fmt.Errorf("data %q is not hex: %w", m[2], err)
			case len(data) != want:
				w.dataErr = fmt.Errorf("data is %d bytes, btmon says %d", len(data), want)
			default:
				w.data = data
			}
		}
	}
	return w
}
