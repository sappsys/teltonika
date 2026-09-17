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
| Tested models | **FMB020**, **FMC920** only |
| Host | Linux (USB CDC serial); other OSes untested |
| Relation to Tracker247 | Separate product; Tracker keeps its own programmer |

Other models may work (same Configurator protocol) but have **not** been verified here.

## What it does

1. Probes USB serial for a Teltonika device (`cfg_info`)
2. Optionally factory-resets (`cfg_default` + save)
3. Reads device defaults (`cfg_getcfg`), programs intersecting parameters
4. Saves, optionally clears/uploads TLS PEMs, verifies

Certificates are deleted **only** if you pass `--clear-certs` or upload with
`--root` / `--key` / `--cert`.

## Build

Requires Go 1.21+ and access to a USB serial device (typically `/dev/ttyUSB0`).

```bash
cd experimental/usb_programmer
chmod +x build.sh
./build.sh
# → ./usb_programmer
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
./usb_programmer --config mydevice.cfg

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
