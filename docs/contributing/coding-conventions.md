---
stability: 1
covers: ["docs/reference/protocol.md"]
---

## Code Style

Follow the conventions of the surrounding code. Consistency within a file
matters more than strict adherence to an external style guide.

## Naming

Use clear, descriptive names. Avoid abbreviations unless they are universally
understood in context.

## Error Handling

Return errors rather than panicking. Wrap errors with context using
fmt.Errorf("context: %w", err).

## Testing

Write a failing test before implementation (TDD). Tests use real data — no
mocks or stubs. Parser and formatter tests use replies captured from a real
controller. Error cases are made by cutting or altering real captures, never
invented from scratch. Bluetooth tests run against the real controller and are skipped
without `GO_PWR_TEST_MAC`.

## Controller safety

`go-pwr` only ever sends the status request (a single space written to
`FFF2`). The Go Power! app also has reset, force-charge and settings commands;
see [the protocol reference](../reference/protocol.md). Never add code that can
send them.
