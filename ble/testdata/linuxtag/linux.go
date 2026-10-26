// ABOUTME: Guard fixture declaring a []byte writer in a //go:build linux file.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

//go:build linux

package ble

func Write(b []byte) {}
