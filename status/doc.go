// ABOUTME: Package documentation for status, the pure logic that turns
// ABOUTME: controller notifications into a reading and a reading into output.

// Package status turns notifications from a Go Power! GP-PWM-30-SB charge
// controller into a reply, a reply into a Reading, and a Reading into text
// or JSON output.
//
// It is pure logic: it needs no Bluetooth and never imports a Bluetooth or
// D-Bus package, so other programs can import it and its tests run on any
// machine.
package status
