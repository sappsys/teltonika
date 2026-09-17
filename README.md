# sappsys/teltonika

Go library for decoding and encoding [Teltonika](https://teltonika-gps.com/) tracker codecs
(TCP/UDP), including Codec 8, 8E, 16, 12, 13, 14 and 15, plus human-readable IO element decoding.

**Module path:** [`github.com/sappsys/teltonika`](https://github.com/sappsys/teltonika)

**Latest release:** [v1.0.5](https://github.com/sappsys/teltonika/releases/tag/v1.0.5) — see [`CHANGELOG.txt`](CHANGELOG.txt)

```bash
go get github.com/sappsys/teltonika@v1.0.5
```

This repository is the **Sappsys / Tracker247** maintained distribution of the Teltonika codec
library. It started as a fork of Alim Zanibekov’s excellent open-source work; after contributing
fixes upstream that were not merged in a timely way, we continue development here under our own
module identity while preserving full credit and the original MIT license terms.

## Used by

- **[Tracker247](https://www.tracker247.co.uk)** — professional GPS fleet tracking
  ([tracker247.co.uk](https://www.tracker247.co.uk)): live maps, journey reports, Teltonika
  hardware, mobile apps, and Home Assistant integration. Production decoding of device telemetry
  for Tracker247 runs on this library (**pinned at `v1.0.5`**).

## Credits

- **Original author:** [Alim Zanibekov](https://github.com/alim-zanibekov)
- **Original repository:** [github.com/alim-zanibekov/teltonika](https://github.com/alim-zanibekov/teltonika)
- **License:** MIT (see [`LICENSE`](LICENSE)) — copyright of the original work remains with
  Alim Zanibekov; subsequent modifications by Sappsys / Tracker247 contributors are also MIT.

We are grateful for the solid codec foundation Alim published. This project would not exist
without that work.

## Why this repository exists

While building and operating [Tracker247](https://www.tracker247.co.uk), we needed reliable
day-to-day Teltonika AVL/IO decoding against current device firmware and wiki documentation.
That led to concrete library improvements, including:

- Aligning AVL and IO element decoding more closely with Teltonika specifications
- Refreshing the `ioelements` catalog from the Teltonika wiki, while preserving historical model
  coverage on regeneration
- Codec and element read fixes exercised in production fleet traffic (for example Codec 8E NX IO
  element handling and signed element reads)

Those changes live in this repository so Tracker247 — and anyone else who wants the same
production-hardened path — can depend on a clearly named, actively maintained module.

## Packages

- `teltonika` — TCP/UDP packet decode/encode for Teltonika tracker codecs
- `ioelements` — map IO element IDs to human-readable names and values (see [`tools`](tools))

### Example

> more examples in [examples](/examples) folder (see [examples/README.md](/examples/README.md))

```go
package main

import (
    "encoding/hex"
    "encoding/json"
    "fmt"

    "github.com/sappsys/teltonika"
    "github.com/sappsys/teltonika/ioelements"
)

func main() {
    packetHex := "00000000000000A98E020000017357633410000F0DC39B2095964A00AC00F80B00000000000B000500F00100150400C8000" +
        "04501007156000500B5000500B600040018000000430FE00044011B000100F10000601B000000000000017357633BE1000F0DC39B209" +
        "5964A00AC00F80B000001810001000000000000000000010181002D11213102030405060708090A0B0C0D0E0F104545010ABC2121020" +
        "30405060708090A0B0C0D0E0F10020B010AAD020000BF30"
    packet, _ := hex.DecodeString(packetHex)
    _, decoded, err := teltonika.DecodeTCPFromSlice(packet)
    if err != nil {
        panic(err)
    }
    res, _ := json.Marshal(decoded)
    fmt.Printf("%s\n", res)

    decoder := ioelements.DefaultDecoder()

    elements := make(map[int][]*ioelements.IOElement)
    for i, data := range decoded.Packet.Data {
        elements[i] = make([]*ioelements.IOElement, len(data.Elements))
        for j, element := range data.Elements {
            elements[i][j], err = decoder.Decode("*", element.Id, element.Value)
            if err != nil {
                panic(err)
            }
        }
    }

    for i, items := range elements {
        fmt.Printf("Frame #%d\n", i)
        for _, element := range items {
            fmt.Printf("    %s\n", element)
        }
    }
}
```

<details>
<summary>Output (byte arrays in hex)</summary>

```json
{
  "packet": {
    "codecId": 142,
    "data": [
      {
        "timestampMs": 1594898986000,
        "lng": 25.2560283,
        "lat": 54.667425,
        "altitude": 172,
        "angle": 248,
        "event_id": 0,
        "speed": 0,
        "satellites": 11,
        "priority": 0,
        "generationType": "Unknown",
        "elements": [
          {
            "id": 240,
            "value": "01"
          },
          {
            "id": 21,
            "value": "04"
          },
          {
            "id": 200,
            "value": "00"
          },
          {
            "id": 69,
            "value": "01"
          },
          {
            "id": 113,
            "value": "56"
          },
          {
            "id": 181,
            "value": "0005"
          },
          {
            "id": 182,
            "value": "0004"
          },
          {
            "id": 24,
            "value": "0000"
          },
          {
            "id": 67,
            "value": "0fe0"
          },
          {
            "id": 68,
            "value": "011b"
          },
          {
            "id": 241,
            "value": "0000601b"
          }
        ]
      },
      {
        "timestampMs": 1594898988001,
        "lng": 25.2560283,
        "lat": 54.667425,
        "altitude": 172,
        "angle": 248,
        "event_id": 385,
        "speed": 0,
        "satellites": 11,
        "priority": 0,
        "generationType": 255,
        "elements": [
          {
            "id": 385,
            "value": "11213102030405060708090a0b0c0d0e0f104545010abc212102030405060708090a0b0c0d0e0f10020b010aad"
          }
        ]
      }
    ]
  },
  "response": "00000002"
}
```

```text
Frame #0
    Movement: true
    GSM Signal: 4
    Sleep Mode: 0
    GNSS Status: 1
    Battery Level: 86%
    GNSS PDOP: 0.500
    GNSS HDOP: 0.400
    Speed: 0km/h
    Battery Voltage: 4.064V
    Battery Current: 0.283A
    Active GSM Operator: 24603
Frame #1
    Beacon: 11213102030405060708090a0b0c0d0e0f104545010abc212102030405060708090a0b0c0d0e0f10020b010aad
```

</details>

### API

Package `teltonika`

Data structures:

```go
package teltonika

type PacketResponse []byte

type DecodedUDP struct {
    PacketId    uint16         // Packet ID
    AvlPacketId uint8          // AVL Packet ID
    Imei        string         // Device IMEI
    Packet      *Packet        // Decoded Packet
    Response    PacketResponse // Response to received packet
}

type DecodedTCP struct {
    Packet   *Packet        // Decoded Packet
    Response PacketResponse // Response to received packet (4 bytes, len(Packet.Data))
}

type Packet struct {
    CodecID  CodecId   // Codec ID, if 8, 8E or 16 Data field is not nil, if 12, 13 or 14 Messages field is not nil
    Data     []Data    // Packet AVLData array
    Messages []Message // Packet Messages array (max 1 message)
}

type Data struct {
    TimestampMs    uint64         // UNIX timestamp in milliseconds
    Lng            float64        // Longitude, east – west position
    Lat            float64        // Latitude, north – south position
    Altitude       int16          // Meters above sea level
    Angle          uint16         // Angle in degrees from the North Pole (clock-wise)
    EventID        uint16         // If data is acquired on event this field contains IOElement id else 0
    Speed          uint16         // Speed calculated from satellites (km/h)
    Satellites     uint8          // Number of visible satellites
    Priority       uint8          // Priority (0 Low, 1 High, 2 Panic)
    GenerationType GenerationType // Codec 16 generation type
    Elements       []IOElement    // Array containing IO Elements
}

type IOElementValue []byte

type IOElement struct {
    Id    uint16         // IO element ID
    Value IOElementValue // Value of the element (for codec 16 and 8 1-8 bytes, for codec 8E 1-X bytes)
}

type Message struct {
    Timestamp uint32      // UNIX timestamp in milliseconds (codecs 13,15)
    Type      MessageType // Type (Command or Response)
    Imei      string      // Device IMEI (codecs 14,15)
    Text      string      // Command or Response represented as string
}

// DecodeConfig optional configuration that can be passed in all Decode* functions (last param).
// By default, used - DecodeConfig { ioElementsAlloc: OnHeap }
type DecodeConfig struct {
    ioElementsAlloc IOElementsAlloc // IOElement->Value allocation mode: `OnHeap` or `OnReadBuffer`
}

```

Methods:

```go
package teltonika

// DecodeTCPFromSlice
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) tcp packet from slice
// returns the number of bytes read from 'inputBuffer' and decoded packet or an error
func DecodeTCPFromSlice(inputBuffer []byte, config ...*DecodeConfig) (int, *DecodedTCP, error)

// DecodeTCPFromReader
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) tcp packet from io.Reader
// returns decoded packet or an error
func DecodeTCPFromReader(input io.Reader, config ...*DecodeConfig) ([]byte, *DecodedTCP, error)

// DecodeTCPFromReaderBuf
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) tcp packet from io.Reader
// writes the read bytes to readBytes buffer (max packet size 1280 bytes)
// returns the number of bytes read and decoded packet or an error
func DecodeTCPFromReaderBuf(input io.Reader, readBytes []byte, config ...*DecodeConfig) (int, *DecodedTCP, error)

// DecodeUDPFromSlice
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) udp packet from slice
// returns the number of bytes read from 'inputBuffer' and decoded packet or an error
func DecodeUDPFromSlice(inputBuffer []byte, config ...*DecodeConfig) (int, *DecodedUDP, error)

// DecodeUDPFromReader
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) udp packet from io.Reader
// returns the read buffer and decoded packet or an error
func DecodeUDPFromReader(input io.Reader, config ...*DecodeConfig) ([]byte, *DecodedUDP, error)

// DecodeUDPFromReaderBuf
// decode (12, 13, 14, 15, 8, 16, or 8 extended codec) udp packet from io.Reader
// writes read bytes to readBytes slice (max packet size 1280 bytes)
// returns the number of bytes read and decoded packet or an error
func DecodeUDPFromReaderBuf(input io.Reader, readBytes []byte, config ...*DecodeConfig) (int, *DecodedUDP, error)

// EncodePacketTCP
// encode packet (12, 13, 14, 15, 8, 16, or 8 extended codec)
// returns an array of bytes with encoded data or an error
// note: implementations for 8, 16, 8E are practically not needed, they are made for testing
func EncodePacketTCP(packet *Packet) ([]byte, error)

// EncodePacketUDP
// encode packet (12, 13, 14, 15, 8, 16, or 8 extended codec)
// returns an array of bytes with encoded data or an error
// note: all implementations are practically not needed, they are made for testing
func EncodePacketUDP(imei string, packetId uint16, avlPacketId uint8, packet *Packet) ([]byte, error)
```

Package `ioelements`

Data structures:

```go
package ioelements

type IOElementDefinition struct {
    Id              uint16      // I/O Element id
    Name            string      // Element name
    NumBytes        int         // Bytes count
    Type            ElementType // Element type (Signed, Unsigned, HEX, ASCII)
    Min             float64     // Min value if number
    Max             float64     // Max value if number
    Multiplier      float64     // Multiplier (used for numbers)
    Units           string      // Element units
    Description     string      // Element full description
    SupportedModels []string    // List of device names that support this I/O element
    Groups          []string    // Element groups
}

type IOElement struct {
    Id         uint16               // I/O Element id
    Value      interface{}          // Value (float64, int64, uint64, string)
    Definition *IOElementDefinition // I/O Element definition
}
```

Methods:

```go
package ioelements

// NewDecoder create new Decoder
func NewDecoder(definitions []IOElementDefinition)

// DefaultDecoder returns a decoder with I/O Element definitions represented in `ioelements_dump.go` file
func DefaultDecoder()

// GetElementInfo returns full description of I/O Element by its id and model name
// If you don't know the model name, you can skip the model name check by passing '*' as the model name
func (r *Decoder) GetElementInfo(modelName string, id uint16) (*IOElementDefinition, error)

// Decode decodes an I/O Element by model name and id (result can be represented in numan-readable format)
// If you don't know the model name, you can skip the model name check by passing '*' as the model name
func (r *Decoder) Decode(modelName string, id uint16, buffer []byte) (*IOElement, error)

// DecodeByDefinition decodes an I/O Element according to a given definition
func (r *Decoder) DecodeByDefinition(def *IOElementDefinition, buffer []byte) (*IOElement, error)
```

### Simple benchmarks (go test -bench)

Command:

```shell
go test -bench=. -benchmem
```

Output:

```text
goos: darwin
goarch: arm64
pkg: github.com/sappsys/teltonika
BenchmarkTCPDecode-10                                            2556680               472.8 ns/op           405 B/op         11 allocs/op
BenchmarkTCPDecodeReader-10                                      2203923               546.8 ns/op           517 B/op         13 allocs/op
BenchmarkUDPDecodeSlice-10                                       1582856               697.8 ns/op          1350 B/op         38 allocs/op
BenchmarkUDPDecodeReader-10                                      1571578               779.7 ns/op          1550 B/op         40 allocs/op
BenchmarkTCPDecodeAllocElementsOnReadBuffer-10                   2882876               410.8 ns/op           382 B/op          5 allocs/op
BenchmarkTCPDecodeReaderAllocElementsOnReadBuffer-10             2506252               478.1 ns/op           498 B/op          7 allocs/op
BenchmarkUDPDecodeSliceAllocElementsOnReadBuffer-10              3202860               374.4 ns/op          1264 B/op          6 allocs/op
BenchmarkUDPDecodeReaderAllocElementsOnReadBuffer-10             2589747               473.6 ns/op          1468 B/op          8 allocs/op
BenchmarkEncodeTCP-10                                           16615262                75.58 ns/op           36 B/op          1 allocs/op
BenchmarkEncodeUDP-10                                           22332055                53.40 ns/op           48 B/op          1 allocs/op
BenchmarkCrc16IBMGenerateLookupTable-10                          4424582               270.1 ns/op           512 B/op          1 allocs/op
BenchmarkCrc16IBMWithLookupTable-10                               455336              2641 ns/op               0 B/op          0 allocs/op
BenchmarkCrc16IBMWithoutLookupTable-10                             47612             25130 ns/op               0 B/op          0 allocs/op
PASS
ok      github.com/sappsys/teltonika     22.685s
```

As you can see from the results, passing the `&teltonika.DecodeConfig{teltonika.OnReadBuffer}`
parameter to the Decode* function noticeably speeds them up, but this prevents the
garbage collector from removing the byte array from which the packet
was read until all references to it (Packet->Data->Elements->Value) are removed,
so this option should be used if long-term packet storage in
RAM is not required (almost always?)

## Experimental: USB Configurator programmer

> **Experimental / unsupported.**  
> [`experimental/usb_programmer`](experimental/usb_programmer) is a **standalone**
> Linux CLI that programs Teltonika devices over USB (Configurator text + FMBX
> protocol). It can factory-reset, write parameters, and upload/delete TLS PEMs.
>
> - **Tested on FMB020, FMC920, and FMP100** (FMP1:1 / FW 04.00.00)
> - Separate `go.mod` — not part of the stable codec API
> - See [experimental/usb_programmer/README.md](experimental/usb_programmer/README.md)
>   for build, warnings, and usage
>
> Do **not** treat this as production-ready for untested models.
