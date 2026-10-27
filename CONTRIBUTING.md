# Contributing

`go-pwr` reads live status from a Go Power! GP-PWM-30-SB solar charge controller over Bluetooth LE. Start with [decision 0001](docs/decisions/0001-go-pwr-design.md) for the design (its assessment is in [docs/assessments/go-pwr-0001.md](docs/assessments/go-pwr-0001.md)), and [the protocol reference](docs/reference/protocol.md) for how the controller talks. The [architecture overview](docs/architecture/overview.md) shows how the code fits together and how `go-pwr` runs on lola. Then read the [development workflow](docs/contributing/development-workflow.md) and the [coding conventions](docs/contributing/coding-conventions.md).

## Short Links to Important Resources

- [Decisions](docs/decisions) — architecture decision records (ADRs)
- [Architecture](docs/architecture) — how the command, reply assembly, decoding, formatting and the Bluetooth layer fit together, and how go-pwr is deployed on lola
- [Assessments](docs/assessments) — the record of each decision's review
- [Contributing guides](docs/contributing) — coding conventions and development workflow
- [Reference](docs/reference) — reference documentation, including the controller protocol
