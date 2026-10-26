// ABOUTME: Guard fixture declaring exported functions taking a pointer to bytes.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

func Send(b *[]byte) {}

func SendOne(b *byte) {}
