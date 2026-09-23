# Experimental: USB Configurator programmer

> **EXPERIMENTAL / UNSUPPORTED**  
> This tool talks to Teltonika trackers over USB using the same text + FMBX
> protocol as Teltonika Configurator. It **writes configuration and can upload
> or delete TLS certificates**. Use at your own risk. It is **not** part of the
> stable `github.com/sappsys/teltonika` codec API.

## Status

| Item | Detail |
|------|--------|
| Maturity | Experimental |
| Tested models | See table below |
| Host | Linux / Windows (USB CDC serial) |
| Relation to Tracker247 | Separate product; Tracker keeps its own programmer |

| Model | Device report (`cfg_info`) |
|-------|----------------------------|
| **FMB020** | FMB0:6 / FW 04.00.00.Rev.550 |
| **FMC920** | FMC9:1 / FW 04.00.00 (Rev from field 13 when present) |
| **FMP100** | FMP1:1 / FW 04.00.00 (Rev from field 13 when present) |

`cfg_info:0` is the short firmware; **`cfg_info:13` is the revision** (e.g. `550` → `Rev.550`).
`cfg_info:1` is the config/protocol version (e.g. `12.00.00`).
`cfg_info:8` is the Configurator keyword flag: **`0` = keyword set (may lock until unlocked)**, `1` = none.
Unlock on the wire is `:sec_login:<keyword>` (→ `<SECSTAT>…`). This tool checks `:sec_status`
and, if locked, prompts once (or use `--keyword WORD`). Ctrl-C aborts — do not guess
(few tries). Use `-v` to dump all `cfg_info` fields.

Device-side certificate paths are always (local PEM filenames are ignored):

- `z:\cert\root.pem`
- `z:\cert\certificate.pem.crt`
- `z:\cert\private.pem.key`

Other models may work (same Configurator protocol) but have **not** been verified here.

## What it does

1. Probes USB serial for a Teltonika device (`cfg_info`)
2. Unlocks Configurator keyword when needed (`--keyword` or interactive prompt)
3. Optionally factory-resets (`cfg_default` + save)
4. Reads device defaults (`cfg_getcfg`), programs intersecting parameters
5. Saves, optionally clears/uploads TLS PEMs, verifies

Certificates are deleted **only** if you pass `--clear-certs` or upload with
`--root` / `--key` / `--cert`.

This tool does **not** flash firmware.

## Build

Requires Go 1.21+ and access to a USB serial device (typically `/dev/ttyUSB0`).

```bash
cd experimental/usb_programmer
chmod +x build.sh
./build.sh
# → ./usb_programmer and ./usb_programmer.exe
```

Or:

```bash
cd experimental/usb_programmer
CGO_ENABLED=0 go test ./usb/
CGO_ENABLED=0 go build -o usb_programmer .
```

## Run

```bash
# Minimal example config (edit IDs/values for your device)
./usb_programmer --config example/example.txt

# Configurator .cfg (gzip) also accepted
./usb_programmer --config mydevice.cfg --keyword WORD

# With TLS PEMs (clears existing certs, then uploads)
./usb_programmer --config example/example.txt \
  --root root.pem --key private.pem.key --cert certificate.pem.crt

# Clear certs only (no factory reset)
./usb_programmer --clear-certs

# Useful flags
./usb_programmer --config example/example.txt --retries 2 -v
./usb_programmer --config example/example.txt --port /dev/ttyUSB0 --timeout 30s
```

### Permissions

Your user must read/write the serial device (often `dialout` / `uucp` group on Linux):

```bash
sudo usermod -aG dialout "$USER"   # then re-login
ls -l /dev/ttyUSB* /dev/ttyACM*
```

## Config format

**Plain text** (`.txt`): one `id:value` per line; `#` comments allowed. See
[`example/example.txt`](example/example.txt).

**Configurator `.cfg`**: gzip-compressed key:value stream as exported by
Teltonika Configurator.

Parameters the device does not advertise after reset are **skipped** (listed in
the end-of-run summary).

## Warnings

- Can wipe or replace device configuration and certificates.
- Do not use on production fleets without bench testing.
- Firmware / model differences can change accepted parameter IDs.
- Per-step timeouts default to **20s**; use `--retries` for flaky USB links.
- This directory is intentionally **self-contained** (own `go.mod`) and does
  not depend on the parent codec module.

## Layout

```
experimental/usb_programmer/
  main.go           CLI
  usb/              USB Configurator protocol library
  example/          Sample plain-text config
  build.sh
  go.mod
  README.md         (this file)
```

## License

MIT — same as the parent [`sappsys/teltonika`](https://github.com/sappsys/teltonika) repository.
