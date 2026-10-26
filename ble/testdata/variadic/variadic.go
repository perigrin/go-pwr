// ABOUTME: Guard fixture declaring an exported function with a variadic ...byte parameter.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

func Send(b ...byte) {}
