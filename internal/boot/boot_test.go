// ABOUTME: Tests parsing the kernel's boot_id and /proc/uptime files, using
// ABOUTME: real copies taken from lola and altered copies of them.

package boot

import (
	"embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// testdata is embedded so the cross-compiled test binary carries it to lola.
//
//go:embed testdata
var testdata embed.FS

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestParseBootIDRejectsMalformed(t *testing.T) {
	real := readTestdata(t, "boot_id-lola")
	if _, err := parseBootID(real); err != nil {
		t.Fatalf("parseBootID(real %q): %v", real, err)
	}
	for _, altered := range []string{
		"",
		"\n",
		real[:20],
		real[:35] + "\n",
		strings.ReplaceAll(real, "-", ""),
		strings.Replace(real, real[:1], "g", 1),
		strings.ToUpper(real),
		"x" + real,
	} {
		if id, err := parseBootID(altered); err == nil {
			t.Errorf("parseBootID(%q) = %q, want an error", altered, id)
		}
	}
}

func TestParseUptimeRejectsMalformed(t *testing.T) {
	real := readTestdata(t, "uptime-lola")
	if v, err := parseUptime(real); err != nil || v <= 0 {
		t.Fatalf("parseUptime(real %q) = %v, %v", real, v, err)
	}
	first := strings.Fields(real)[0]
	for _, altered := range []string{
		"",
		"\n",
		strings.Replace(real, first, "abc", 1),
		strings.Replace(real, first, "-"+first, 1),
		strings.Replace(real, first, "NaN", 1),
		strings.Replace(real, first, "Inf", 1),
	} {
		if v, err := parseUptime(altered); err == nil {
			t.Errorf("parseUptime(%q) = %v, want an error", altered, v)
		}
	}
}

// parseUptime returns the first number in the contents of /proc/uptime,
// seconds since boot on the boottime clock. The Linux tests compare Uptime
// against it, so it must never read a malformed file as 0.
func parseUptime(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("uptime %q is empty", s)
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, fmt.Errorf("uptime %q: %w", s, err)
	}
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("uptime %q is not a non-negative number of seconds", s)
	}
	return v, nil
}
