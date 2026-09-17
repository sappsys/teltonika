package usb

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCRC16IBM_FMBXListFrame(t *testing.T) {
	// From trackercap.pcap: seq=22 op=0x28 payload=0200070002 CRC=a1ab
	payload, _ := hex.DecodeString("0200070002")
	frame := FMBX(22, 0x0028, payload)
	want, _ := hex.DecodeString("464d425800000016002800050200070002a1ab")
	if hex.EncodeToString(frame) != hex.EncodeToString(want) {
		t.Fatalf("got %x want %x", frame, want)
	}
}

func TestCertSetupPayload_RootSize(t *testing.T) {
	// Synthetic PEM of length 1407 with known CRC from live root.pem was 0x9e7f —
	// here just check size encoding and slot.
	pem := make([]byte, 1407)
	body := CertSetupPayload(pem, CertSlotRoot)
	if len(body) != 29 {
		t.Fatalf("body len %d", len(body))
	}
	if body[6] != 0x05 || body[7] != 0x7f {
		t.Fatalf("size bytes %02x%02x", body[6], body[7])
	}
	if body[28] != CertSlotRoot {
		t.Fatalf("slot %d", body[28])
	}
}

func TestParseCfgInfo(t *testing.T) {
	text := "cfg_info:0:04.00.00\rcfg_info:1:12.00.00\rcfg_info:2:FMB0:6\rcfg_info:3:350612074908128\rcfg_info:8:0\rcfg_info:13:550\r"
	info := ParseCfgInfo(text)
	if info.FW != "04.00.00" || info.FWRev != "550" || info.ConfigVer != "12.00.00" || info.HWFamily != "FMB0" || info.HWVariant != "6" || info.IMEI != "350612074908128" {
		t.Fatalf("%+v", info)
	}
	if !info.KeywordConfigured || !info.MayBeKeywordLocked() {
		t.Fatalf("expected keyword configured from cfg_info:8=0")
	}
	if info.FWFull() != "04.00.00.Rev.550" {
		t.Fatalf("FWFull %q", info.FWFull())
	}
	if GuessModel(info) != "FMB020" {
		t.Fatalf("guess %q", GuessModel(info))
	}
	unlocked := ParseCfgInfo("cfg_info:8:1\r")
	if unlocked.KeywordConfigured || unlocked.MayBeKeywordLocked() {
		t.Fatalf("cfg_info:8=1 should mean no keyword")
	}
}

func TestIdentifyLabel(t *testing.T) {
	label := IdentifyLabel(DeviceInfo{IMEI: "123", HWFamily: "FMB0", HWVariant: "6", FW: "04.00.00", FWRev: "550", ConfigVer: "12.00.00"})
	want := "IMEI 123 / FMB0:6 / FW 04.00.00.Rev.550"
	if label != want {
		t.Fatalf("got %q want %q", label, want)
	}
}

func TestFormatCfgInfoVerbose(t *testing.T) {
	info := ParseCfgInfo("cfg_info:0:04.00.00\rcfg_info:13:550\r")
	got := FormatCfgInfoVerbose(info)
	if !strings.Contains(got, "0 firmware: 04.00.00") || !strings.Contains(got, "13 firmware revision: 550") {
		t.Fatalf("%q", got)
	}
}

func TestFilterParamsToDevice(t *testing.T) {
	defaults := map[string]string{"101": "2", "2004": "", "999": "1"}
	want := []Param{
		{ID: "101", Value: "14"},
		{ID: "2004", Value: ""},
		{ID: "888", Value: "1"},
	}
	fr := FilterParamsToDevice(want, defaults)
	if len(fr.Keep) != 1 || fr.Keep[0].ID != "101" || fr.Keep[0].Value != "14" {
		t.Fatalf("keep=%v", fr.Keep)
	}
	if len(fr.Unsupported) != 1 || len(fr.Unchanged) != 1 {
		t.Fatalf("unsupported=%d unchanged=%d", len(fr.Unsupported), len(fr.Unchanged))
	}
}

func TestVerifyParams(t *testing.T) {
	want := []Param{{ID: "101", Value: "14"}, {ID: "2004", Value: "x"}, {ID: "11004", Value: "4.1"}}
	got := map[string]string{"101": "14", "2004": "y", "11004": "4.100000"}
	m := VerifyParams(want, got)
	if len(m) != 1 || m[0] != `2004: want "x" got "y"` {
		t.Fatalf("%v", m)
	}
}

func TestCertListProbe(t *testing.T) {
	if certListProbe(CertSlotRoot) != 2 || certListProbe(CertSlotDevice) != 3 || certListProbe(CertSlotPrivate) != 4 {
		t.Fatalf("probe mapping")
	}
	ok, _ := hex.DecodeString("0001000200020000000910107a3a5c636572745c726f6f742e70656d")
	empty, _ := hex.DecodeString("0001000200020003")
	if !certListed(ok) || certListed(empty) {
		t.Fatalf("certListed")
	}
	if extractCertPath(ok) != CertPathRoot {
		t.Fatalf("path %q", extractCertPath(ok))
	}
}

func TestExtractCertPath_IgnoresPrintableCRC(t *testing.T) {
	// Full FMBX 0x29 list reply: path private.pem.key then CRC bytes 'X''G' (0x5847).
	frame, _ := hex.DecodeString(
		"464d42580000000d00290023" + // FMBX seq=13 op=0x29 n=35
			"000100020002000000091017" +
			"7a3a5c636572745c707269766174652e70656d2e6b6579" + // z:\cert\private.pem.key
			"5847", // printable CRC tail that previously corrupted the path
	)
	got := extractCertPath(frame)
	if got != CertPathPrivate {
		t.Fatalf("got %q want %q", got, CertPathPrivate)
	}
}

func TestCertDeletePayload(t *testing.T) {
	// DeleteCerts.pcap seq=587: delete root.pem
	got := CertDeletePayload(CertPathRoot)
	want, _ := hex.DecodeString("00000110107a3a5c636572745c726f6f742e70656d")
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("got %x want %x", got, want)
	}
	got = CertDeletePayload(CertPathDevice)
	want, _ = hex.DecodeString("000001101b7a3a5c636572745c63657274696669636174652e70656d2e637274")
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("device got %x want %x", got, want)
	}
}

func TestIsStepTimeout(t *testing.T) {
	err := fmt.Errorf("wrap: %w", &StepTimeoutError{Step: "Connecting", Timeout: time.Second})
	if !IsStepTimeout(err) {
		t.Fatal("expected step timeout")
	}
	if IsStepTimeout(fmt.Errorf("nope")) {
		t.Fatal("unexpected")
	}
}


