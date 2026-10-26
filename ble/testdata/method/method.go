// ABOUTME: Guard fixture declaring an exported method with a []byte parameter.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

type Adapter struct{}

func (a *Adapter) Send(b []byte) error { return nil }
