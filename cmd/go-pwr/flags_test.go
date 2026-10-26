// ABOUTME: Tests for go-pwr's command-line parsing: flag values, defaults and
// ABOUTME: the combinations that are legal.

package main

import (
	"io"
	"testing"
	"time"
)

func TestFlagValues(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		poll, follow time.Duration
	}{
		{"neither", nil, 0, 0},
		{"bare poll", []string{"--poll"}, 30 * time.Second, 0},
		{"bare follow", []string{"--follow"}, 0, 5 * time.Second},
		{"poll true", []string{"--poll=true"}, 30 * time.Second, 0},
		{"follow true", []string{"--follow=true"}, 0, 5 * time.Second},
		{"poll duration", []string{"--poll=10s"}, 10 * time.Second, 0},
		{"follow duration", []string{"--follow=500ms"}, 0, 500 * time.Millisecond},
		{"poll minutes", []string{"--poll=2m"}, 2 * time.Minute, 0},
		{"poll false", []string{"--poll=false"}, 0, 0},
		{"follow false", []string{"--follow=false"}, 0, 0},
		{"poll false with follow", []string{"--poll=false", "--follow"}, 0, 5 * time.Second},
		{"follow false with poll", []string{"--follow=false", "--poll"}, 30 * time.Second, 0},
		{"both false", []string{"--poll=false", "--follow=false"}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseArgs(tt.args, io.Discard)
			if err != nil {
				t.Fatalf("parseArgs(%q) error: %v", tt.args, err)
			}
			if cfg.poll != tt.poll || cfg.follow != tt.follow {
				t.Errorf("parseArgs(%q) poll=%v follow=%v, want poll=%v follow=%v",
					tt.args, cfg.poll, cfg.follow, tt.poll, tt.follow)
			}
		})
	}
}

func TestFlagDefaults(t *testing.T) {
	cfg, err := parseArgs(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.format != "text" || cfg.timeout != 20*time.Second || cfg.mac != "" {
		t.Errorf("defaults = %+v, want format text, timeout 20s, no mac", cfg)
	}
}
