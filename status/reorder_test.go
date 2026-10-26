// ABOUTME: Proves that chunks delivered out of order raise warnings, using
// ABOUTME: real captures with their chunks swapped; labelled altered or derived.

package status

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// afternoonCorpus names the body-only afternoon capture's corpus.
const afternoonCorpus = "afternoon body-only, 20-byte chunks"

// reorderCorpus is a real reply as chunks, to be reordered.
type reorderCorpus struct {
	name   string
	chunks [][]byte
	// framed is whether the chunks are a framed reply, which the Assembler
	// judges. The afternoon capture is the 166-byte body alone, so it is
	// judged by the body rule alone (wholeBody) and is not passed off as a
	// whole framed reply (owner decision 12).
	framed bool
}

// reorderCorpora returns the real replies whose chunks the tests reorder:
// the body-only afternoon reply cut every 20 bytes (derived chunking, as it
// is not a notification sequence), and the lola replies as the 9
// notifications the controller sent. Reordered copies are altered data.
func reorderCorpora(t *testing.T) []reorderCorpus {
	t.Helper()
	cs := []reorderCorpus{{name: afternoonCorpus, chunks: splitEvery(capturedReply(t), 20)}}
	for n := 1; n <= 21; n++ {
		cs = append(cs, reorderCorpus{name: fmt.Sprintf("lola reply-%02d", n), chunks: lolaSequence(t, n), framed: true})
	}
	for _, c := range cs {
		if len(c.chunks) != 9 {
			t.Fatalf("%s: %d chunks, want 9", c.name, len(c.chunks))
		}
	}
	return cs
}

// reordered is what came of delivering a corpus's chunks in a permuted order.
type reordered struct {
	whole     bool  // the reply passed the whole rule
	asmErr    error // why the Assembler failed the read, for a framed corpus
	reply     string
	reading   Reading
	decodeErr error // set when the reply was whole but Decode failed it
}

// caught is whether the reordering was stopped by a check short of the
// in-order guard: not whole, failed by Decode, or decoded with a warning.
func (r reordered) caught() bool {
	return !r.whole || r.decodeErr != nil || len(r.reading.Warnings) > 0
}

// deliver delivers the corpus's chunks in the order perm, chunk perm[0]
// first, with keys 1, 2, ... as ble would assign them to notifications
// received in that order, so the key order does not itself catch the swap.
func deliver(t *testing.T, c reorderCorpus, perm []int) reordered {
	t.Helper()
	var chunks [][]byte
	for _, k := range perm {
		chunks = append(chunks, c.chunks[k])
	}
	var out reordered
	if c.framed {
		a := NewAssembler()
		tok := a.Begin(0)
		for i, ch := range chunks {
			var err error
			out.reply, out.whole, err = a.Add(tok, uint64(i+1), ch)
			if err != nil {
				out.asmErr = err
				out.whole = false
				return out
			}
		}
	} else {
		var b strings.Builder
		for _, ch := range chunks {
			b.Write(ch)
		}
		out.reply = b.String()
		out.whole = wholeBody(out.reply)
	}
	if out.whole {
		out.reading, out.decodeErr = Decode(out.reply)
	}
	return out
}

// readFile returns the contents of name, failing the test if it can't.
func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// swap returns the identity order of 9 chunks with i and j exchanged.
func swap(i, j int) []int {
	p := identity()
	p[i], p[j] = p[j], p[i]
	return p
}

func identity() []int {
	p := make([]int, 9)
	for k := range p {
		p[k] = k
	}
	return p
}

// hasWarning is whether r carries a warning matching target.
func hasWarning(r Reading, target error) bool {
	for _, w := range r.Warnings {
		if errors.Is(w, target) {
			return true
		}
	}
	return false
}

// swapTally counts the 36 pairwise swaps of one corpus by outcome.
type swapTally struct {
	whole, decodeFails, decoded, temp, width, both, silent int
	silentSwaps                                            []string
}

func tallySwaps(t *testing.T, c reorderCorpus) swapTally {
	t.Helper()
	var s swapTally
	for i := 0; i < 9; i++ {
		for j := i + 1; j < 9; j++ {
			o := deliver(t, c, swap(i, j))
			if !o.whole {
				continue
			}
			s.whole++
			if o.decodeErr != nil {
				s.decodeFails++
				continue
			}
			s.decoded++
			tm, w := hasWarning(o.reading, ErrTempMismatch), hasWarning(o.reading, ErrFieldWidth)
			switch {
			case tm && w:
				s.both++
			case !tm && !w:
				s.silent++
				s.silentSwaps = append(s.silentSwaps, fmt.Sprintf("%d<->%d (battery %d mV)", i, j, o.reading.BatteryMV))
			}
			if tm {
				s.temp++
			}
			if w {
				s.width++
			}
		}
	}
	return s
}

// TestReorderedChunksRaiseWarnings swaps every pair of chunks of the real
// replies. Every swap that passes the whole rule must either fail Decode or
// carry a warning, except the one swap only in-order delivery prevents:
// chunks 0 and 3 of the body-only afternoon reply, which
// TestReorderSwap03ReportsZeroVolts pins.
func TestReorderedChunksRaiseWarnings(t *testing.T) {
	for _, c := range reorderCorpora(t) {
		for i := 0; i < 9; i++ {
			for j := i + 1; j < 9; j++ {
				o := deliver(t, c, swap(i, j))
				if c.name == afternoonCorpus && i == 0 && j == 3 {
					continue
				}
				if o.whole && o.decodeErr == nil && len(o.reading.Warnings) == 0 {
					t.Errorf("%s: swap %d<->%d passes whole and decodes with no warning (battery %d mV)",
						c.name, i, j, o.reading.BatteryMV)
				}
			}
		}
	}
}

// TestReorderSwapTally pins how the 36 pairwise swaps of each real reply end:
// how many pass the whole rule, how many of those fail Decode, and how many
// of the rest each backstop catches. docs/reference/protocol.md, Reordered
// notifications, records these counts.
func TestReorderSwapTally(t *testing.T) {
	cs := reorderCorpora(t)

	// Body-only afternoon reply, cut every 20 bytes. Swaps that fail Decode
	// move a '+' or extra digits into a gating field.
	got := tallySwaps(t, cs[0])
	want := swapTally{whole: 31, decodeFails: 4, decoded: 27, temp: 13, width: 26, both: 13, silent: 1,
		silentSwaps: []string{"0<->3 (battery 0 mV)"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: tally = %+v, want %+v", cs[0].name, got, want)
	}

	// Framed lola replies: the Assembler fails the 8 swaps with chunk 0,
	// which holds the leading space, and the 7 that move the CRLF chunk
	// (8) away from the end. The other 21 all pass whole, all decode and
	// every one trips the width backstop.
	for _, c := range cs[1:] {
		got := tallySwaps(t, c)
		if got.whole != 21 || got.decodeFails != 0 || got.width != 21 || got.silent != 0 {
			t.Errorf("%s: tally = %+v, want 21 whole, 0 Decode failures, 21 width, 0 silent", c.name, got)
		}
	}
	got = tallySwaps(t, cs[1])
	if got.temp != 10 || got.both != 10 {
		t.Errorf("%s: tally = %+v, want 10 caught by the °C/°F check as well", cs[1].name, got)
	}
}

// TestReorderSwap03ReportsZeroVolts is the one swap only in-order delivery
// prevents. Chunks 0 and 3 of the body-only afternoon reply, exchanged, make
// a body that is whole, decodes, and puts a zero field where the 13.24 V
// battery was: the gating battery field reads 00000, which docs/reference/
// protocol.md, Plausibility bounds ("A battery field of 00000 is accepted"),
// records as accepted. No warning fires either. The same swap of a real
// framed lola reply never gets that far: chunk 0 holds the leading space, so
// the Assembler refuses the reply as not framed.
func TestReorderSwap03ReportsZeroVolts(t *testing.T) {
	cs := reorderCorpora(t)

	// The record this test follows is still in protocol.md.
	doc := readFile(t, "../docs/reference/protocol.md")
	if !strings.Contains(doc, "**A battery field of `00000` is accepted.**") {
		t.Fatal("protocol.md, Plausibility bounds, no longer records that a battery field of 00000 is accepted; update this test with it")
	}

	o := deliver(t, cs[0], swap(0, 3))
	if !o.whole || o.decodeErr != nil {
		t.Fatalf("afternoon swap 0<->3: whole=%v Decode error=%v, want a whole reply that decodes", o.whole, o.decodeErr)
	}
	if o.reading.BatteryMV != 0 {
		t.Errorf("BatteryMV = %d, want 0 (the healthy 13244 mV read as 0.00 V)", o.reading.BatteryMV)
	}
	if len(o.reading.Warnings) != 0 || len(o.reading.FieldErrors) != 0 {
		t.Errorf("warnings %v, field errors %v: want none, only in-order delivery prevents this swap", o.reading.Warnings, o.reading.FieldErrors)
	}

	for _, c := range cs[1:] {
		o := deliver(t, c, swap(0, 3))
		if !errors.Is(o.asmErr, ErrNotFramed) {
			t.Errorf("%s: swap 0<->3 error = %v, want ErrNotFramed", c.name, o.asmErr)
		}
	}
}

// reorderings returns every reversal, rotation and adjacent triple
// permutation of the 9 chunks, named. A triple permutation reorders three
// consecutive chunks any of the five other ways and leaves the rest.
func reorderings() map[string][]int {
	out := map[string][]int{}
	rev := identity()
	for k := range rev {
		rev[k] = 8 - k
	}
	out["reversal"] = rev
	for r := 1; r < 9; r++ {
		p := identity()
		for k := range p {
			p[k] = (k + r) % 9
		}
		out[fmt.Sprintf("rotation by %d", r)] = p
	}
	for i := 0; i+2 < 9; i++ {
		for _, t := range [][3]int{{0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
			p := identity()
			for k := 0; k < 3; k++ {
				p[i+k] = i + t[k]
			}
			out[fmt.Sprintf("chunks %d-%d as %v", i, i+2, t)] = p
		}
	}
	return out
}

// undetectedReorderings names the reversals, rotations and triple
// permutations that pass the whole rule, decode, and raise no warning. None
// exists among the real captures; a change that adds one must name it here.
var undetectedReorderings = map[string]bool{}

// TestReorderedRotationsAndReversal reorders the real chunks more than a
// swap does. Each ordering that passes the whole rule must fail Decode or
// raise a warning, or be named in undetectedReorderings.
func TestReorderedRotationsAndReversal(t *testing.T) {
	for _, c := range reorderCorpora(t) {
		whole := 0
		for name, perm := range reorderings() {
			o := deliver(t, c, perm)
			if o.whole {
				whole++
			}
			if o.whole && o.decodeErr == nil && len(o.reading.Warnings) == 0 && !undetectedReorderings[c.name+": "+name] {
				t.Errorf("%s: %s passes whole and decodes with no warning (battery %d mV)", c.name, name, o.reading.BatteryMV)
			}
		}
		if whole == 0 {
			t.Errorf("%s: no reordering passed the whole rule, so the test proves nothing", c.name)
		}
	}
}

// TestReorderWarningsDoNotGate checks that on every pairwise swap that
// passes the whole rule and decodes, the warnings come with the reading: no
// error, the gating fields as the reordered bytes spell them, and the reply
// kept whole in Raw. A swap that carries both a °C/°F and a width warning
// keeps both.
func TestReorderWarningsDoNotGate(t *testing.T) {
	sawBoth := false
	for _, c := range reorderCorpora(t) {
		for i := 0; i < 9; i++ {
			for j := i + 1; j < 9; j++ {
				o := deliver(t, c, swap(i, j))
				if !o.whole || o.decodeErr != nil {
					continue
				}
				r := o.reading
				fields := strings.Split(replyBody(o.reply), ";")
				for _, g := range []struct {
					name  string
					index int
					got   int
				}{{"charge current", indexChargeMA, r.ChargeMA}, {"battery voltage", indexBatteryMV, r.BatteryMV}, {"PV voltage", indexSolarMV, r.SolarMV}} {
					if want, err := parseUnsigned(fields[g.index]); err != nil || want != g.got {
						t.Errorf("%s swap %d<->%d: %s = %d, want %q read as it stands", c.name, i, j, g.name, g.got, fields[g.index])
					}
				}
				if r.Raw != o.reply {
					t.Errorf("%s swap %d<->%d: Raw is not the reordered reply", c.name, i, j)
				}
				if hasWarning(r, ErrTempMismatch) && hasWarning(r, ErrFieldWidth) {
					sawBoth = true
				}
			}
		}
	}
	if !sawBoth {
		t.Error("no swap carried both warnings, so the test did not show them coexisting")
	}
}
