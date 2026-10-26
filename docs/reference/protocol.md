# Go Power! GP-PWM-30-SB Bluetooth protocol

How to read live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT, a Dometic brand, fitted at the factory to many Forest River trailers) over Bluetooth LE.

## Sources and confidence

- **Code:** the Go Power! Connect **Android** app, `com.gpelectric.gopowermonitor` 1.7.23 (version code 78), decompiled with jadx 1.5.6. The relevant classes are `pmwbattery.newCode.PwmSbBatteryManager` (requests and replies), `pmwbattery.newCode.PwmResponseParser` (field decoding), `pmwbattery.PwmBatteryUtil` (code tables), `defpackage.factoryReset` (other commands), and `com.brainx.bxble` (the BLE layer).
- **Observation:** one status reply, captured on 2026-10-25 from one controller set to AGM, while it was bulk-charging in the afternoon. It was compared with the Status screen of the **iPhone** app (version not recorded), read about seven minutes earlier: battery 13.27 V, PV 22.80 V, 0.60 A, 12 Ah, AGM, Bulk, +72°F. The reply gave 13.24 V, 22.88 V, 0.66 A, 12 Ah, AGM, code 0, +72°F.
- **The two apps don't agree.** The Android code labels charge code 0 "Boost", but the iPhone screen said "Bulk". The Android code parses indices 8, 14 and 19–23 as **hexadecimal**, so `00012` would show 18 Ah, but the iPhone screen showed 12 Ah. A jump from 12 to 18 Ah in seven minutes at 0.6 A is impossible. That shows the **iPhone app** reads index 19 as decimal. It doesn't prove what the controller means, because each app shows its own interpretation. This document uses decimal. Logging `raw` until amp-hours today passes 9 will settle it: `00010` means decimal, `0000A` means hex.
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

## BlueZ errors observed on lola

Measured on lola (Raspberry Pi Zero W, BlueZ, Open reading `org.bluez.Adapter1.Powered` on `/org/bluez/hci0` over the system bus). The `ble` package tells these apart by D-Bus error name with `errors.As`, never by message text.

| Condition | D-Bus error name | Notes |
|---|---|---|
| hci0 unbound from `hci_uart_bcm` `serial0-0` (no adapter, or the adapter vanished) | `org.freedesktop.DBus.Error.UnknownObject` | The message says `Method "Get" ... doesn't exist`. |
| BlueZ stopped by `systemctl mask --runtime bluetooth.service` then `stop` | `org.freedesktop.DBus.Error.FileExists` | The message says `Unit dbus-org.bluez.service failed to load properly ... File exists`. It arrives about 15 seconds after the call, not at once. It is not `ServiceUnknown`: BlueZ is D-Bus activated through the `dbus-org.bluez.service` alias, so a plain `stop` is undone by the next call, and masking the real unit makes the activation fail with this name. `ServiceUnknown` and `NameHasNoOwner` are classified as not running too, but were not seen. |
| `nobody` under a bus policy that denies `send_destination="org.bluez"` | `org.freedesktop.DBus.Error.AccessDenied` | The message starts `Rejected send message, 3 matched rules`. |
| Adapter powered off (`bluetoothctl power off`) or soft-blocked (`rfkill block bluetooth`) | none | The adapter stays on the bus with `Powered` false. Powered off and blocked look the same to `Open`. |

If the system bus itself can't be reached (tested with `DBUS_SYSTEM_BUS_ADDRESS` pointing at a missing socket), the dial error is not classified: nothing observed says what it means for BlueZ. The error is still fatal at startup.

The bluetooth library hard-codes `hci0`. If an adapter reset brought the adapter back as `hci1`, retries would fail until the program restarts. That is unlikely, because Linux reuses the lowest free index; unbinding and rebinding the driver on lola brought it back as `hci0`.

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
| 16 | Temperature | °C | +22 | consistent with °F (the app showed only °F) |
| 17 | Temperature | °F | +72 | observed once |
| 19 | Amp-hours today | Ah | 12 | observed once |
| 20 | Amp-hours yesterday | Ah | 15 | from code |
| 21 | Amp-hours day before yesterday | Ah | 16 | from code |
| 22 | Amp-hours, "three days before" | Ah | 4 | from code |
| 23 | Amp-hours, "seven days before" | Ah | 93 | from code |

The app screens can't settle whether indices 19–23 are decimal or hex, because each app shows its own interpretation. Logging `raw` until amp-hours today passes 9 will (see **Sources and confidence**). What period each of indices 20–23 covers is a separate open question.

Indices 1, 3, 4, 6, 7, 13, 15, 18 and 24–31 are discarded or unused by the app, and their meaning is unknown. Index 3 (14475) looks like another millivolt value, perhaps a charge target, but that's a guess.

### Operation flags (index 5)

The app reads the two lowest bits:

- **Bit 1:** charging when set. The example value 3 came while charging at 0.66 A.
- **Bit 0:** set when the display unit is not Celsius. The example, with the app showing °F, fits.

The app decodes no fault codes. Its failure list is always empty in this build.

### Charge state codes (index 12)

From the Android app's `PwmBatteryUtil.getChargingMode`. Its exact strings, misspellings included, are below. Only code 0 has been observed. Codes 1–7 come from the Android code alone, and the iPhone app may label them differently: it showed "Bulk" for code 0.

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

### Plausibility bounds

Only three fields can fail a reading on their values: charge current (index 0), battery voltage (index 2) and PV voltage (index 11). Each must be an unsigned decimal of one to five ASCII digits, the width the controller sends, and lie inside the bounds below, inclusive. The bounds come from what the field format can carry, never from the range seen in captures, and must never reject a real reading, however low.

| Field | Lower bound | Upper bound |
|---|---|---|
| Charge current | 0 mA | 99,999 mA |
| Battery voltage | 0 mV | 99,999 mV |
| PV voltage | 0 mV | 99,999 mV |

**Why not the controller's ratings.** The GP-PWM-30-SB User Manual (2019, sections 1.2 and 1.6) rates the controller for 35 VDC solar input and 37.5 A continuous input current, and its battery input at 9.0 to 15.5 VDC. These are tolerances, not limits on what the controller reports. An array's open-circuit voltage rises on a cold morning and can pass 35 V, and current can spike past the rating at the edge of a cloud. Because a gating field out of bounds fails the whole reading, a bound at a rating would suppress the battery voltage the shutdown script needs whenever PV or current exceeded it. The only limit certain to hold is the field's width, so that is the bound. Below it, nothing in a field's value can show that the field is misaligned. That guard is in-order delivery (decision 0001, *Shape of the code*).

**A battery field of `00000` is accepted.** The manual's 9.0 to 15.5 VDC battery input range is a rating, not a promise that the controller stops reporting below it, and the decision forbids rejecting a real reading however low. The 0.00 V reading that one chunk swap produces is therefore not caught here. In-order delivery from the Bluetooth layer is what prevents it.

A sign of either kind, an empty field, embedded spaces, hexadecimal or non-ASCII digits, or more than five digits fail the reading.

### °C/°F check

When both temperature fields (indices 16 and 17) decode, the reported °F must lie within **2.6 °F** of 9/5 × reported °C + 32. A larger gap adds a warning to the reading, and the reading is still returned unchanged. The check is skipped when either field is null.

The controller reports each temperature as a whole number, and which way it rounds or truncates is not known. The tolerance comes from sweeping every true temperature from −40 to +60 °C in 0.01 °C steps, with °C and °F each independently half-up, half-even, half-away, truncated toward zero or floored. The worst gap over the whole sweep is 2.6 °F, so the tolerance is exactly that:

| Gap | Case |
|---|---|
| 2.6 °F | True −38.99 °C (−38.18 °F). °C truncated toward zero gives −38 (expected −36.4 °F) and °F floored gives −39. This is the worst mix: °C truncated toward zero and °F floored, which err in opposite directions below zero. Any rounding mode at all stays within 2.6 °F: each whole-degree report is off by less than 1 °C (1.8 °F) and 1 °F respectively, and the errors can add, so the gap is under 2.8 °F, and in fifths of a degree it is a whole number. |
| 2.0 °F | True −0.99 °C (30.2 °F). °C truncated to `0` (expected 32 °F) and °F truncated to `30`. The worst case at freezing. |
| 1.4 °F | The worst gap when both fields are rounded, in any of the three rounding modes. |
| 0.4 °F | The real capture: `+22` °C and `+72` °F against an expected 71.6 °F. |

5 × °F − (9 × °C + 160) is always a whole number, so the code compares it as an integer against 13 (2.6 × 5) with no float rounding. "At the tolerance" means a gap of exactly 2.6 °F, which some °C values can reach (22 °C with 69 °F, 3 °C with 40 °F); that gives no warning, and any larger gap does.

**The check only warns for now.** It has not been confirmed against the cold and hot captures, which are still to be taken (decision 0001). Making it fail readings is a separate, later change.
## Notification sizes and timing on lola

Measured on 2026-10-25 at 22:08–22:10 EDT on lola (Raspberry Pi Zero W, BCM43438, BlueZ 5.82, kernel 6.18), about 2 m from the controller (RSSI −68 dBm), at night: battery 13.18–13.19 V, PV 0.65 V, no charge current. The trace is `status/testdata/lola-timing/session-1.btsnoop`, its `btmon -r` decoding is beside it, and each reply's notifications are in `session-1-reply-NN.seq.txt` (line 1 names the trace; every other line is microseconds since the request write, then the payload in hex). 21 replies: four fresh connections of two replies each, then one held connection of 13. BlueZ's GATT cache was cleared first (`bluetoothctl remove`), so the first connection includes service discovery. Confidence: **observed** on this one night, one adapter and one controller.

**Framing: the reply is not what the 2026-10-25 afternoon capture shows.** Every one of the 21 replies is 169 bytes: a leading space (`0x20`, which looks like the controller echoing the request), the 166-byte body ending in `;`, then `\r\n`. For example:

```
 00000;000;13187;14525;00000;00003;00183;39402;00008;003;100;00645;000;00000;00019;00049;+20;+68;+20;00000;00015;00015;00016;00080;00133;00000;02309;02025;009;025;008;\r\n
```

Split on `;`, that is still 32 fields, but the first is ` 00000` and the last is `\r\n`, not empty. The afternoon capture in **Example reply** is the 166-byte body alone. How it was taken is not recorded, so whether the controller sent the space and `\r\n` then is unknown. A trailing `\r\n` would be a natural end-of-reply marker.

- **ATT MTU: 23.** BlueZ offers 517 in Exchange MTU, and the controller answers 23. So notifications carry at most 20 bytes, like the phone app's.
- **Sizes:** every reply is nine notifications, 8 × 20 bytes plus one of 9.
- **Timing:** notifications arrive in pairs, one pair per 30 ms connection event (the controller asks for a 20–30 ms interval). From the request write to the first notification: 33–96 ms, median 48 ms. To the last: 123–202 ms, median 142 ms. Gaps between notifications in a reply: median 1.4 ms, 90th percentile 29.5 ms, longest 88 ms. Across 168 gaps: 103 under 4 ms, 42 near 30 ms, 7 near 22.5 ms, 7 near 7.5 ms, and 9 between 37.5 and 90 ms.
- **Connect (first look, by `bluetoothctl connect` and polling `ServicesResolved` in a script; the go-pwr budget is measured in the connect issue):** the controller was seen 1.3 s into a scan. From connect to services resolved: 5.9 s with discovery, then 5.5, 2.5, 2.6 and 3.5 s with the cache warm.

### Handle map

From the discovery on the first connection (Read By Group Type, Read By Type and Find Information):

| Handle | What |
|---|---|
| `0x0001`–`0x0007` | Generic Access service (`0x1800`): Device Name value `0x0003`, Appearance `0x0005`, Peripheral Preferred Connection Parameters `0x0007` |
| `0x0008`–`0x000b` | Generic Attribute service (`0x1801`): Service Changed declaration `0x0009`, value `0x000a`, its CCCD `0x000b` |
| `0x000c`–`0xffff` | Service `FFF0` |
| `0x000d` / `0x000e` | `FFF1` declaration / value (notify) |
| `0x000f` | `FFF1` CCCD |
| `0x0010` / `0x0011` | `FFF2` declaration / value (write, write without response) |

There is **no `FFE1` GATT service**. `FFE1` appears only as the advertisement's service-data UUID.

### Writes in the trace

Every write in the trace, by handle:

- `0x0011` (`FFF2`): 21 Write Requests, each exactly `20`.
- `0x000f` (`FFF1` CCCD): `0100` (notifications on) 5 times, `0000` (off) 5 times.
- `0x000b` (Service Changed CCCD): `0200` (indications on) once per connection, 5 times. **BlueZ writes this itself**, without being asked.
- BlueZ did **not** write Client Supported Features (`0x2B29`). The controller's GATT service has no such characteristic.

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
- Then comes a long run in which `0x50` recurs every 16 bytes, followed by zeros. Reading these as history records is an interpretation the dump can't confirm.
- Two captures a minute apart were byte-for-byte identical, so the dump does not track live readings. Some values in it are near live ones (`59 A3` = 22,947 mV is close to the PV voltage), but they don't change between captures and may be history such as a daily maximum.

That project's parser reads one of those 16-byte runs, and it takes "battery percent" from the `0x50` byte that starts it, so it always reports 80%. Use the status request above for live data.
