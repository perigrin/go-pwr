# Go Power! GP-PWM-30-SB Bluetooth protocol

How to read live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT, a Dometic brand, fitted at the factory to many Forest River trailers) over Bluetooth LE.

## Sources and confidence

- **Code:** the Go Power! Connect **Android** app, `com.gpelectric.gopowermonitor` 1.7.23 (version code 78), decompiled with jadx 1.5.6. The relevant classes are `pmwbattery.newCode.PwmSbBatteryManager` (requests and replies), `pmwbattery.newCode.PwmResponseParser` (field decoding), `pmwbattery.PwmBatteryUtil` (code tables), `defpackage.factoryReset` (other commands), and `com.brainx.bxble` (the BLE layer).
- **Observation:** one status reply, captured on 2026-10-25 from one controller set to AGM, while it was bulk-charging in the afternoon. It was compared with the Status screen of the **iPhone** app (version not recorded), read about seven minutes earlier: battery 13.27 V, PV 22.80 V, 0.60 A, 12 Ah, AGM, Bulk, +72°F. The reply gave 13.24 V, 22.88 V, 0.66 A, 12 Ah, AGM, code 0, +72°F.
- **The two apps don't agree.** The Android code labels charge code 0 "Boost", but the iPhone screen said "Bulk". The Android code parses indices 8, 14 and 19–23 as **hexadecimal**, so `00012` would show 18 Ah, but the iPhone screen showed 12 Ah. A jump from 12 to 18 Ah in seven minutes at 0.6 A is impossible, so the observation supports **decimal**, and this document uses decimal.
- Confidence labels below: **observed once** means it matched the iPhone screen in that single reply. **From code** means it comes only from the Android code or its variable names. Nothing here has been seen across charge states, battery types or temperatures yet.

## Discovery

The controller advertises:

- **Name:** `GP-PWM-30-SB`. The app also knows `GP-PWM-30-DB`, a dual-bank model, which this document does not cover.
- **Manufacturer data:** company `0x0639` (Shenzhen Minew Technologies), payload ASCII `Minew Tech`. The Bluetooth radio is a Minew BLE-to-serial module.
- **Service data** for UUID `0xFFE1`: the device's own 6-byte MAC address.

The advertisements carry **no readings**. You have to connect.

## Connection

| Service | Characteristic | Use |
|---|---|---|
| `0000fff0-0000-1000-8000-00805f9b34fb` | `0000fff2-…` | writes to the controller |
| | `0000fff1-…` | notifications from the controller |

A `0000ffe1-…` service is also present. The app doesn't use it for this controller.

- **What the app does:** it requests a bond (`createBond`), enables notifications on both `FFF2` and `FFF1`, waits 2 s after connecting before its first request, then polls every 3 s. It never requests a larger MTU, so it receives 20-byte notifications.
- **What has been observed to work:** in one test, a plain connection with no bond, notifications on `FFF1` only, and an immediate request returned a full reply.
- The controller appears to allow **one connection at a time**. While the phone app was connected, other connection attempts failed. That was observed once.

## Status request

1. Enable notifications on `FFF1`.
2. Write the single byte `0x20` (an ASCII space) to `FFF2`. Write-without-response works.
3. Concatenate the notification payloads from `FFF1`, in order.

**How the app decides the reply is complete:** it expects 9 notifications, or gives up after 2 s with whatever has arrived. Only then does it split the text on `;` and accept 30, 31 or 32 fields. If the count is wrong, it keeps showing the previous reading. With 20-byte notifications, the observed 166-byte reply is exactly 9 notifications. After 8 of them, the text already splits into 30 fields, the last a truncated `02`. So the app's acceptance of 30 or 31 fields may be tolerance of a cut-off reply rather than evidence that other firmware sends fewer fields. Notification sizes depend on the negotiated MTU, which BlueZ may raise, so a different client will see different chunking.

The observed reply has **32 fields**: 31 values and a trailing `;`.

No unlock is needed. The app uses its unlock sequence only from the settings screen.

### Example reply

```
00655;200;13244;14475;00000;00003;00000;39026;00008;003;100;22879;000;00000;00019;00049;+22;+72;+22;00012;00015;00016;00004;00093;00132;00000;02306;02025;009;025;008;
```

### Fields

Indices are 0-based. Values are decimal (see **Sources and confidence**) and may have leading zeros or a `+`/`-` sign.

| Index | Meaning | Scale | Example | Confidence |
|---|---|---|---|---|
| 0 | Charge current | mA | 655 → 0.66 A | observed once |
| 2 | Battery voltage | mV | 13244 → 13.24 V | observed once |
| 5 | Operation flags | bit field, see below | 3 | partly observed |
| 8 | Firmware | — | 8 | from code (Android reads it as hex) |
| 9 | Battery type | code, see below | 3 → AGM | observed once |
| 10 | **Unknown.** The Android code calls it "percent" and uses it only to choose the battery icon. | — | 100 | It read 100 while the battery was bulk-charging at 13.24 V, so it is probably **not** state of charge. |
| 11 | PV (solar) voltage | mV | 22879 → 22.88 V | observed once |
| 12 | Charge state | code, see below | 0 | observed once (shown as "Bulk") |
| 14 | Serial | — | 19 | from code (Android reads it as hex, which would make this 25) |
| 16 | Temperature | °C | +22 | observed once |
| 17 | Temperature | °F | +72 | observed once |
| 19 | Amp-hours today | Ah | 12 | observed once |
| 20 | Amp-hours yesterday | Ah | 15 | from code |
| 21 | Amp-hours day before yesterday | Ah | 16 | from code |
| 22 | Amp-hours, "three days before" | Ah | 4 | from code |
| 23 | Amp-hours, "seven days before" | Ah | 93 | from code |

The app's History screen would settle indices 20–23: it shows 15/16/4/93 if they are decimal, 21/22/4/147 if hex.

Indices 1, 3, 4, 6, 7, 13, 15, 18 and 24–31 are discarded or unused by the app, and their meaning is unknown. Index 3 (14475) looks like another millivolt value, perhaps a charge target, but that's a guess.

### Operation flags (index 5)

The app reads the two lowest bits:

- **Bit 1:** charging when set. The example value 3 came while charging at 0.66 A.
- **Bit 0:** set when the display unit is not Celsius. The example, with the app showing °F, fits.

The app decodes no fault codes. Its failure list is always empty in this build.

### Charge state codes (index 12)

From the Android app's `PwmBatteryUtil.getChargingMode`. Its exact strings, misspellings included, are below. The iPhone app may label them differently: it showed "Bulk" for code 0.

| Code | Android string |
|---|---|
| 0 | `Boost` (iPhone shows **Bulk**) |
| 1 | `Float` |
| 2 | `Equalization` |
| 3 | `Abosrbtion` |
| 4 | `Bulk Equalization` |
| 5 | `Charge Current` |
| 6 | `Charge Voltage` |
| 7 | `NoCharge` |

### Battery type codes (index 9)

From `PwmBatteryUtil.getBatteryType`: 0 `Sealed`, 1 `FLOODED`, 2 `LIFEPO`, 3 `AGM`, 4 `CUSTOM`, 5 `Not Used`.

## Other writes: do not send

`FFF2` is the input to a serial bridge. The app's other commands are ASCII strings sent **one character per write**, so the controller is parsing a character stream. That makes any stray byte on `FFF2` potentially part of a command. A monitoring client should write only the single byte `0x20`.

The app's other commands start with these prefixes. They are listed so a reader can recognise them, not so they can be sent:

| Prefix | Purpose |
|---|---|
| `&G` | unlock |
| `&I` | read settings pages |
| `&K` | force absorption / float |
| `&L` | factory reset, soft reset, history reset |
| `&H` | temperature unit, battery type |
| `&N` | individual settings |

The Modbus request described below is also a write path. It was sent to the controller once, at slave addresses 1–8, during this investigation. It is read-only in intent, but it still goes through the same serial bridge.

## What doesn't work: the Modbus request

A project online (`bigthrilla/camper-ble-bridge`) polls this controller by writing a Modbus RTU "read holding registers" request to `FFF2`: `[addr, 0x03, 0x01, 0x00, 0x00, 0x21]` plus CRC-16, at slave address 1. The controller answers with about 3 KB of `;`-separated **hex** bytes:

- It begins with a header and a block of charge settings in millivolts. For example, `38 40` is 14,400 mV, the AGM absorption voltage.
- Then come dozens of 16-byte history records, each beginning with `0x50`, then zeros.
- Two captures a minute apart were byte-for-byte identical, so the dump does not track live readings. Some values in it are near live ones (`59 A3` = 22,947 mV is close to the PV voltage), but they don't change between captures and may be history such as a daily maximum.

That project's parser reads one of the history records, and it takes "battery percent" from the record's `0x50` marker byte, so it always reports 80%. Use the status request above for live data.
