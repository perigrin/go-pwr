// ABOUTME: Guard fixture declaring an exported function that takes a []byte.
// ABOUTME: It is parsed by TestNoExportedByteWriter and never built.

package ble

func Write(b []byte) {}
