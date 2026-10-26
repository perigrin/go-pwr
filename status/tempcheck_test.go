// ABOUTME: Tests the °C/°F agreement check: a sweep of every rounding and
// ABOUTME: truncation mix, plus altered captures around the tolerance.

package status

import (
	"errors"
	"testing"
)

// A rounding mode turns the exact fraction n/d (d > 0) into a whole degree.
type roundingMode struct {
	name string
	fn   func(n, d int) int
}

func floorDiv(n, d int) int {
	q := n / d
	if n%d != 0 && n < 0 {
		q--
	}
	return q
}

func truncDiv(n, d int) int { return n / d }

func roundHalfUp(n, d int) int { return floorDiv(2*n+d, 2*d) }

func roundHalfAway(n, d int) int {
	if n < 0 {
		return -roundHalfAway(-n, d)
	}
	return roundHalfUp(n, d)
}

func roundHalfEven(n, d int) int {
	q := floorDiv(n, d)
	switch r := 2 * (n - q*d); {
	case r < d:
		return q
	case r > d:
		return q + 1
	case q%2 == 0:
		return q
	default:
		return q + 1
	}
}

var roundingModes = []roundingMode{
	{"half-up", roundHalfUp},
	{"half-even", roundHalfEven},
	{"half-away", roundHalfAway},
	{"truncate toward zero", truncDiv},
	{"floor", floorDiv},
}

// TestTempToleranceCoversRoundingAndTruncation sweeps every true
// temperature from −40 to +60 °C in 0.01 °C steps, reports each field with
// every mix of rounding or truncation, and checks the gap between the
// reported °F and 9/5·reported °C + 32 stays within the tolerance. The
// sweep is in integers: k is hundredths of a degree C, so the true °F is
// (9k+16000)/500.
func TestTempToleranceCoversRoundingAndTruncation(t *testing.T) {
	worst, worstK := 0, 0
	var worstC, worstF string
	for k := -4000; k <= 6000; k++ {
		for _, cm := range roundingModes {
			for _, fm := range roundingModes {
				c := cm.fn(k, 100)
				f := fm.fn(9*k+16000, 500)
				if g := tempGapFifths(c, f); g > worst {
					worst, worstK, worstC, worstF = g, k, cm.name, fm.name
				}
			}
		}
	}
	t.Logf("worst gap %d/5 °F at %.2f °C (°C %s, °F %s)", worst, float64(worstK)/100, worstC, worstF)
	if worst > tempToleranceFifths {
		t.Errorf("tolerance %d/5 °F is below the worst gap %d/5 °F at %.2f °C (°C %s, °F %s)",
			tempToleranceFifths, worst, float64(worstK)/100, worstC, worstF)
	}
	if worst < tempToleranceFifths {
		t.Errorf("tolerance %d/5 °F is looser than the worst gap %d/5 °F; tighten it", tempToleranceFifths, worst)
	}
	// The −0.99 °C case from the protocol notes: °C truncated to 0, °F to 30.
	if g := tempGapFifths(0, 30); g > tempToleranceFifths {
		t.Errorf("0 °C / 30 °F gap %d/5 °F exceeds tolerance %d/5", g, tempToleranceFifths)
	}
}

// tempWarnings returns the temperature warnings on r, failing the test on
// any warning that is not one.
func tempWarnings(t *testing.T, r Reading) []*TempMismatchWarning {
	t.Helper()
	var ws []*TempMismatchWarning
	for _, w := range r.Warnings {
		var tw *TempMismatchWarning
		if !errors.As(w, &tw) || !errors.Is(w, ErrTempMismatch) {
			t.Errorf("unexpected warning %v", w)
			continue
		}
		ws = append(ws, tw)
	}
	return ws
}

func TestTempCheckCapturedReplyNoWarning(t *testing.T) {
	r, err := Decode(capturedReply(t))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("real capture gave warnings: %v", r.Warnings)
	}
}

func TestTempMismatchWarnsOnly(t *testing.T) {
	r, err := Decode(alteredReply(t, indexTempF, "+80"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	ws := tempWarnings(t, r)
	if len(ws) != 1 {
		t.Fatalf("got %d warnings %v, want 1", len(r.Warnings), r.Warnings)
	}
	if ws[0].TempC != 22 || ws[0].TempF != 80 {
		t.Errorf("warning = %+v, want 22 °C / 80 °F", ws[0])
	}
	if r.TempF == nil || *r.TempF != 80 || r.ChargeMA != 655 {
		t.Errorf("reading not returned intact: %+v", r)
	}
}
