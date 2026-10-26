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

Write a failing test before implementation (TDD). Tests use real data first.
A hand-built fake (D-Bus signals, property maps, a fake device) is allowed only
where a real condition can't be produced reliably, and never to prove that a real
reading is accepted. Parser and formatter tests use replies captured from a real
controller. Error cases are made by cutting or altering real captures, never
invented from scratch. Bluetooth tests run against the real controller and are skipped
without `GO_PWR_TEST_MAC`.

## Controller safety

`go-pwr` only ever sends the status request (a single space written to
`FFF2`). The Go Power! app also has reset, force-charge and settings commands;
see [the protocol reference](../reference/protocol.md). Never add code that can
send them.

The `ble` package exports no function, method or interface method that accepts
bytes to write, whether as `byte`, `[]byte`, `...byte` or a defined type
built on them. An address or mode is a typed value, never raw bytes.
`TestNoExportedByteWriter` in `ble/api_test.go` enforces this by parsing every
non-test `.go` file in `ble/` with build tags ignored, so the Linux files are
checked on the Mac. Unexported helpers may take `[]byte`.

The guard reads parameter lists only, so it is a backstop, not proof. It does
not see bytes reaching the controller through:

- exported struct fields, or a struct parameter with a `[]byte` field;
- exported variables or fields of function type, such as
  `var Write = func([]byte) {}`, or function-typed parameters and results;
- embedded types, such as an exported struct embedding `io.Writer`;
- generic type parameters, such as `func Send[T ~[]byte](p T)`;
- types from other packages, such as `json.RawMessage`, or the Bluetooth
  library's `DeviceCharacteristic`, whose `Write` and `WriteWithoutResponse`
  take `[]byte`;
- `string`, `[]rune`, `io.Reader` or `any` parameters, which can carry bytes as
  well as `[]byte` can;
- packages below `ble/`, which it doesn't scan.

Review of every `ble` change must check these by hand: nothing exported may
let a caller choose what is written, and the library's characteristic types
must never be exported.
