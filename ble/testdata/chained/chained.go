// ABOUTME: Guard fixture declaring a defined type built on another defined byte-slice type.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

type raw []byte

type Payload raw

func Send(p Payload) {}
