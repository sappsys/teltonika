// Package usb talks to Teltonika trackers over USB CDC serial
// using the Configurator text + FMBX protocol captured from USBPcap.
package usb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// CRC16IBM is CRC-16/ARC (poly 0xA001, init 0), used on FMBX frames.
func CRC16IBM(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// FMBX builds a framed message: magic + seq + opcode + n + payload + CRC16-IBM(BE).
// n is len(payload); CRC is over bytes from seq through payload.
func FMBX(seq uint32, opcode uint16, payload []byte) []byte {
	buf := make([]byte, 0, 12+len(payload)+2)
	buf = append(buf, 'F', 'M', 'B', 'X')
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], seq)
	binary.BigEndian.PutUint16(hdr[4:6], opcode)
	binary.BigEndian.PutUint16(hdr[6:8], uint16(len(payload)))
	buf = append(buf, hdr[:]...)
	buf = append(buf, payload...)
	c := CRC16IBM(buf[4:])
	buf = append(buf, byte(c>>8), byte(c))
	return buf
}

// CertSlot identifies which PEM file is uploaded (Configurator order).
const (
	CertSlotRoot    = 0
	CertSlotDevice  = 1
	CertSlotPrivate = 2
)

// Device-side paths used by Configurator for TLS PEMs (DeleteCerts.pcap).
const (
	CertPathRoot    = `z:\cert\root.pem`
	CertPathDevice  = `z:\cert\certificate.pem.crt`
	CertPathPrivate = `z:\cert\private.pem.key`
)

// CertSetupPayload builds FMBX opcode 0x28 body for a PEM upload.
func CertSetupPayload(pem []byte, slot int) []byte {
	size := len(pem)
	nchunks := (size + 511) / 512
	fcrc := CRC16IBM(pem)
	return []byte{
		0x01, 0x00,
		0x02, 0x05, 0x00, 0x00, byte(size >> 8), byte(size), 0x00,
		0x03, 0x05, 0x00, 0x00, 0x00, byte(nchunks), 0x00,
		0x04, 0x03, 0x02, 0x00, 0x00,
		0x05, 0x03, byte(fcrc >> 8), byte(fcrc), 0x00,
		0x06, 0x00, byte(slot),
	}
}

// CertDeletePayload builds FMBX opcode 0x28 body to delete a stored cert by path.
// From DeleteCerts.pcap: 00 00 01 10 <len> <ascii path>
func CertDeletePayload(path string) []byte {
	p := []byte(path)
	body := make([]byte, 5+len(p))
	body[0], body[1] = 0x00, 0x00
	body[2], body[3] = 0x01, 0x10
	body[4] = byte(len(p))
	copy(body[5:], p)
	return body
}

// DeviceInfo is parsed from :cfg_info:? response.
// Configurator queries ":cfg_info:?" (not per-field). Observed fields on FMB020:
//
//	0 firmware (04.00.00)  1 config/protocol (12.00.00)  2 hardware (FMB0:6)
//	3 IMEI  8 keyword (0=configured / may lock Configurator, 1=none)
//	12 build  13 firmware revision (550 → Rev.550)  14 modem/module string
//
// Keyword.pcap: cfg_info:8:0 when Configurator prompts for a keyword; unlocked
// captures use 8:1. Field 8 stays 0 after a successful unlock (keyword still set).
// Unlock on the wire is ":sec_login:<keyword>" → "<SECSTAT>…". Program_Tracker
// prompts interactively when :sec_status shows secured but not authorized.
type DeviceInfo struct {
	Raw       map[int]string
	IMEI      string
	FW        string // cfg_info:0
	FWRev     string // cfg_info:13 (numeric revision without "Rev." prefix)
	ConfigVer string // cfg_info:1 — Configurator/protocol version (e.g. 12.00.00)
	HWFamily  string // e.g. FMB0 from cfg_info:2:FMB0:6
	HWVariant string // e.g. 6
	// KeywordConfigured is true when cfg_info:8 is "0" (Configurator security keyword set).
	KeywordConfigured bool
}

// FWFull returns firmware with revision when known (e.g. "04.00.00.Rev.550").
func (info DeviceInfo) FWFull() string {
	if info.FW == "" {
		return ""
	}
	if info.FWRev == "" {
		return info.FW
	}
	return info.FW + ".Rev." + info.FWRev
}

// MayBeKeywordLocked reports that a Configurator keyword appears to be set
// (cfg_info:8==0). Confirm with a successful cfg_getcfg (non-zero params).
func (info DeviceInfo) MayBeKeywordLocked() bool {
	return info.KeywordConfigured
}

func ParseCfgInfo(text string) DeviceInfo {
	info := DeviceInfo{Raw: map[int]string{}}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "cfg_info:") {
			continue
		}
		rest := strings.TrimPrefix(ln, "cfg_info:")
		idx, val, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(idx, "%d", &n); err != nil {
			continue
		}
		info.Raw[n] = val
	}
	info.FW = strings.TrimSpace(info.Raw[0])
	info.ConfigVer = strings.TrimSpace(info.Raw[1])
	info.FWRev = strings.TrimSpace(info.Raw[13])
	info.IMEI = strings.TrimSpace(info.Raw[3])
	// 0 = keyword configured (may block getcfg until unlocked); 1 = no keyword.
	if v, ok := info.Raw[8]; ok {
		info.KeywordConfigured = v == "0"
	}
	if v := info.Raw[2]; v != "" {
		fam, ver, ok := strings.Cut(v, ":")
		if ok {
			info.HWFamily = fam
			info.HWVariant = ver
		} else {
			info.HWFamily = v
		}
	}
	return info
}

// GuessModel maps cfg_info hardware family to a Configurator FmType when possible.
func GuessModel(info DeviceInfo) string {
	switch strings.ToUpper(info.HWFamily) {
	case "FMB0":
		// Mobility / personal trackers (TM25 etc.) share FMB0 family; FMB020 is the common FmType.
		return "FMB020"
	case "FMC9":
		return "FMC920"
	case "FMB9":
		return "FMB920"
	case "FMB1", "FMB2":
		return ""
	default:
		if info.HWFamily != "" {
			return strings.ToUpper(info.HWFamily)
		}
		return ""
	}
}

// PortCandidate is a serial port that might be a Teltonika USB CDC interface.
type PortCandidate struct {
	Name         string
	IsUSB        bool
	VID          string
	PID          string
	Product      string
	SerialNumber string
}

// ListCandidatePorts returns serial ports likely to be Teltonika USB adapters.
func ListCandidatePorts() ([]PortCandidate, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		// Fall back to bare names.
		names, err2 := serial.GetPortsList()
		if err2 != nil {
			return nil, err
		}
		out := make([]PortCandidate, 0, len(names))
		for _, n := range names {
			out = append(out, PortCandidate{Name: n, IsUSB: looksLikeUSBName(n)})
		}
		return out, nil
	}
	var out []PortCandidate
	for _, p := range ports {
		c := PortCandidate{
			Name:         p.Name,
			IsUSB:        p.IsUSB,
			VID:          p.VID,
			PID:          p.PID,
			Product:      p.Product,
			SerialNumber: p.SerialNumber,
		}
		if !c.IsUSB && !looksLikeUSBName(c.Name) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func looksLikeUSBName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "ttyusb") ||
		strings.Contains(n, "ttyacm") ||
		strings.Contains(n, "cu.usb") ||
		strings.HasPrefix(n, "com")
}

// IsTeltonikaVID reports MediaTek/Teltonika USB vendor used by TM25 etc.
func IsTeltonikaVID(vid string) bool {
	return strings.EqualFold(vid, "0E8D") || strings.EqualFold(vid, "0e8d")
}

// Client is an open USB serial session to a tracker.
type Client struct {
	port   serial.Port
	seq    uint32
	logf   func(string, ...any)
	mu     sync.Mutex
	closed bool
}

// StepTimeoutError means a named Program step exceeded its deadline.
type StepTimeoutError struct {
	Step    string
	Timeout time.Duration
}

func (e *StepTimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s", e.Step, e.Timeout)
}

// IsStepTimeout reports whether err is (or wraps) a step deadline expiry.
func IsStepTimeout(err error) bool {
	var t *StepTimeoutError
	return errors.As(err, &t)
}

// DefaultStepTimeout is used when ProgramOptions.StepTimeout is unset.
const DefaultStepTimeout = 20 * time.Second

// Open opens portName at 115200 8N1.
func Open(portName string) (*Client, error) {
	mode := &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	p, err := serial.Open(portName, mode)
	if err != nil {
		return nil, err
	}
	_ = p.SetReadTimeout(100 * time.Millisecond)
	_ = p.SetDTR(true)
	_ = p.SetRTS(true)
	c := &Client{port: p, seq: 1, logf: func(string, ...any) {}}
	return c, nil
}

func (c *Client) SetLogger(fn func(string, ...any)) {
	if fn != nil {
		c.logf = fn
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.port == nil {
		return nil
	}
	c.closed = true
	return c.port.Close()
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// withStep runs fn under a hard deadline. On timeout the serial port is closed
// so a blocked Read/Write cannot hang the process forever.
func (c *Client) withStep(name string, timeout time.Duration, fn func() error) error {
	if timeout <= 0 {
		timeout = DefaultStepTimeout
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("%s: panic: %v", name, r)
			}
		}()
		done <- fn()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = c.Close()
		// Give the worker a moment to notice the closed port.
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
		return &StepTimeoutError{Step: name, Timeout: timeout}
	}
}

func (c *Client) drain(d time.Duration) {
	if c.isClosed() {
		return
	}
	deadline := time.Now().Add(d)
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		if c.isClosed() {
			return
		}
		n, _ := c.port.Read(buf)
		if n == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func (c *Client) readWindow(wait, idle time.Duration) []byte {
	if c.isClosed() {
		return nil
	}
	start := time.Now()
	// Cap total time so continuous UART debug spam cannot hang forever by
	// repeatedly extending the idle deadline.
	absoluteMax := wait + 2*time.Second
	if wait >= 5*time.Second {
		absoluteMax = 45 * time.Second // cfg_getcfg dumps
	}
	hardEnd := start.Add(absoluteMax)
	deadline := start.Add(wait)
	var out []byte
	buf := make([]byte, 8192)
	for {
		if c.isClosed() {
			break
		}
		now := time.Now()
		if !now.Before(deadline) || !now.Before(hardEnd) {
			break
		}
		n, err := c.port.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			next := time.Now().Add(idle)
			if next.After(hardEnd) {
				deadline = hardEnd
			} else {
				deadline = next
			}
			continue
		}
		if err != nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return out
}

// TxRaw writes bytes and reads the reply window.
func (c *Client) TxRaw(data []byte, wait, idle time.Duration) []byte {
	if c.isClosed() {
		return nil
	}
	c.drain(50 * time.Millisecond)
	if c.isClosed() {
		return nil
	}
	_, _ = c.port.Write(data)
	return c.readWindow(wait, idle)
}

// txRawHard wraps TxRaw with an outer deadline and closes the port on expiry so
// a stuck USB Write/Read cannot hang the process.
func (c *Client) txRawHard(data []byte, wait, idle, hard time.Duration) []byte {
	if hard <= wait {
		hard = wait + time.Second
	}
	ch := make(chan []byte, 1)
	go func() {
		ch <- c.TxRaw(data, wait, idle)
	}()
	select {
	case b := <-ch:
		return b
	case <-time.After(hard):
		_ = c.Close()
		select {
		case b := <-ch:
			return b
		case <-time.After(200 * time.Millisecond):
			return nil
		}
	}
}

// TxText sends a Configurator text command ending in CR.
// Uses a hard outer deadline so a wedged USB Write cannot hang forever.
func (c *Client) TxText(cmd string, wait time.Duration) string {
	if !strings.HasSuffix(cmd, "\r") {
		cmd += "\r"
	}
	c.logf("TX %q", strings.TrimSuffix(cmd, "\r"))
	hard := wait + 2*time.Second
	if hard < 3*time.Second {
		hard = 3 * time.Second
	}
	resp := c.txRawHard([]byte(cmd), wait, 350*time.Millisecond, hard)
	text := string(resp)
	for i, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if ln == "" {
			continue
		}
		if i > 15 {
			break
		}
		c.logf("  | %s", truncate(ln, 180))
	}
	return text
}

func (c *Client) TxFMBX(opcode uint16, payload []byte, wait time.Duration) []byte {
	frame := FMBX(c.seq, opcode, payload)
	c.logf("TX FMBX seq=%d op=0x%04x len=%d", c.seq, opcode, len(frame))
	resp := c.TxRaw(frame, wait, 250*time.Millisecond)
	c.seq++
	if len(resp) > 0 {
		c.logf("  RX %d bytes", len(resp))
	}
	return resp
}

func (c *Client) Handshake() {
	for _, body := range [][]byte{{0x03, 0x16}, {0x03, 0x23}, {0x03, 0x2a}} {
		if c.isClosed() {
			return
		}
		c.TxFMBX(0x0038, body, 800*time.Millisecond)
	}
}

// cfgConnect runs Handshake then :cfg_connect until CFG_CONNECT is seen.
// Never sends .log:0 — that Write wedges post-DFU CDC and txRawHard closes the port,
// after which every later TX looks successful in the log but returns "".
func (c *Client) cfgConnect(attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var last string
	for i := 1; i <= attempts; i++ {
		if c.isClosed() {
			return fmt.Errorf("cfg_connect: port closed (prior USB Write wedged)")
		}
		if i > 1 {
			time.Sleep(time.Duration(i) * 400 * time.Millisecond)
		}
		c.drain(150 * time.Millisecond)
		c.Handshake()
		c.drain(150 * time.Millisecond)
		if c.isClosed() {
			return fmt.Errorf("cfg_connect: port closed during handshake")
		}
		last = c.TxText(":cfg_connect", 3*time.Second)
		if strings.Contains(strings.ToUpper(last), "CFG_CONNECT") {
			c.TxFMBX(0x0038, []byte{0x03, 0x10}, 800*time.Millisecond)
			return nil
		}
		c.logf("cfg_connect attempt %d/%d: %q", i, attempts, truncate(last, 80))
	}
	if c.isClosed() {
		return fmt.Errorf("cfg_connect: port closed (prior USB Write wedged)")
	}
	return fmt.Errorf("cfg_connect failed: %q", truncate(last, 120))
}

// Probe opens briefly and checks for a Teltonika response.
// Keyword-locked sessions often silence cfg_info; :sec_status still answers.
func Probe(portName string) (DeviceInfo, error) {
	c, err := Open(portName)
	if err != nil {
		return DeviceInfo{}, err
	}
	defer c.Close()
	c.drain(400 * time.Millisecond)
	_ = c.TxText(".log:0", time.Second)
	text := string(c.TxRaw([]byte(":cfg_info:?\r"), 3*time.Second, 500*time.Millisecond))
	if strings.Contains(text, "cfg_info:") {
		return ParseCfgInfo(text), nil
	}
	c.Handshake()
	stText := c.TxText(":sec_status", 2*time.Second)
	if _, ok := ParseSecStat(stText); ok {
		return DeviceInfo{}, nil
	}
	return DeviceInfo{}, fmt.Errorf("no cfg_info/sec_status on %s", portName)
}

// FindTracker probes candidate ports and returns the first Teltonika device found.
func FindTracker(prefer string) (port string, info DeviceInfo, err error) {
	if prefer != "" {
		info, err = Probe(prefer)
		if err == nil {
			return prefer, info, nil
		}
		return "", DeviceInfo{}, fmt.Errorf("preferred port %s: %w", prefer, err)
	}
	cands, err := ListCandidatePorts()
	if err != nil {
		return "", DeviceInfo{}, err
	}
	// Prefer Teltonika VID, then any USB serial.
	var ordered []PortCandidate
	for _, c := range cands {
		if IsTeltonikaVID(c.VID) {
			ordered = append(ordered, c)
		}
	}
	for _, c := range cands {
		if !IsTeltonikaVID(c.VID) {
			ordered = append(ordered, c)
		}
	}
	var errs []string
	for _, c := range ordered {
		info, err := Probe(c.Name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", c.Name, err))
			continue
		}
		return c.Name, info, nil
	}
	if len(errs) == 0 {
		return "", DeviceInfo{}, fmt.Errorf("no USB serial ports found")
	}
	return "", DeviceInfo{}, fmt.Errorf("no Teltonika tracker found:\n  %s", strings.Join(errs, "\n  "))
}

// Param is one configuration id:value pair.
type Param struct {
	ID, Value string
}

// ProgramOptions controls reset + setparam + optional cert upload.
type ProgramOptions struct {
	Params      []Param
	Root        []byte // optional PEM
	Key         []byte
	Cert        []byte
	ClearCerts  bool          // delete device TLS PEMs (Configurator Security delete)
	SkipReset   bool          // debug: skip cfg_default (+ its save)
	SkipReboot  bool          // debug: skip .reset after successful program
	StepTimeout time.Duration // hard deadline per named step (default DefaultStepTimeout)
	// Progress prints user-facing status lines (no trailing newline added by caller).
	Progress func(string)
	// Verbose prints extra diagnostics (parameter filter summary, counts, etc.).
	Verbose func(string)
	// PromptKeyword asks for the Configurator keyword when the session is locked.
	// Return ("", err) to abort. Nil means unlock cannot be attempted interactively.
	PromptKeyword func() (string, error)
}

func (opt ProgramOptions) progress(msg string) {
	if opt.Progress != nil {
		opt.Progress(msg)
	}
}

func (opt ProgramOptions) verbose(msg string) {
	if opt.Verbose != nil {
		opt.Verbose(msg)
	}
}

func (opt ProgramOptions) stepTimeout(def time.Duration) time.Duration {
	if def <= 0 {
		def = DefaultStepTimeout
	}
	// --timeout extends steps; it must not shrink long stages (getcfg, keyword prompt, …).
	if opt.StepTimeout > def {
		return opt.StepTimeout
	}
	return def
}

// ProgramResult summarizes a programming run.
type ProgramResult struct {
	Info             DeviceInfo
	FileParams       int
	DeviceParams     int
	Programmed       []Param
	Unsupported      []Param // in config file but not accepted by this device
	Unchanged        []Param // already matched post-reset default
	Batches          int
	CertsDeleted     []string
	CertsUploaded    bool
	CertsAfter       []string
	VerifyMismatches []string
}

// Summary returns a multi-line human report (printed at end of every run).
func (r *ProgramResult) Summary() string {
	if r == nil {
		return ""
	}
	model := GuessModel(r.Info)
	var b strings.Builder
	b.WriteString("=== Program summary ===\n")
	if label := IdentifyLabel(r.Info); label != "" {
		fmt.Fprintf(&b, "Device: %s\n", label)
	}
	if model != "" {
		fmt.Fprintf(&b, "Guessed model: %s\n", model)
	}
	fmt.Fprintf(&b, "Config params: %d | device accepts: %d | programmed: %d | already default: %d | not on device: %d\n",
		r.FileParams, r.DeviceParams, len(r.Programmed), len(r.Unchanged), len(r.Unsupported))
	if r.Batches > 0 {
		fmt.Fprintf(&b, "Setparam batches: %d\n", r.Batches)
	}
	if len(r.Unsupported) > 0 {
		b.WriteString("Not programmed (unsupported on this device):\n")
		for _, p := range r.Unsupported {
			fmt.Fprintf(&b, "  %s=%s  (%s)\n", p.ID, p.Value, ParamHint(model, p.ID, p.Value, true))
		}
	}
	if len(r.Unchanged) > 0 && len(r.Unchanged) <= 25 {
		b.WriteString("Skipped (already at default):\n")
		for _, p := range r.Unchanged {
			fmt.Fprintf(&b, "  %s=%s  (%s)\n", p.ID, p.Value, ParamHint(model, p.ID, p.Value, false))
		}
	} else if len(r.Unchanged) > 25 {
		fmt.Fprintf(&b, "Skipped (already at default): %d params\n", len(r.Unchanged))
	}
	if len(r.Programmed) > 0 && len(r.Programmed) <= 40 {
		b.WriteString("Programmed:\n")
		for _, p := range r.Programmed {
			fmt.Fprintf(&b, "  %s=%s  (%s)\n", p.ID, p.Value, ParamHint(model, p.ID, p.Value, false))
		}
	} else if len(r.Programmed) > 40 {
		fmt.Fprintf(&b, "Programmed: %d params\n", len(r.Programmed))
	}
	if len(r.CertsDeleted) > 0 {
		fmt.Fprintf(&b, "Certs deleted: %s\n", strings.Join(r.CertsDeleted, ", "))
	}
	if r.CertsUploaded {
		b.WriteString("Certs uploaded: root + private key + device cert\n")
	}
	if len(r.CertsAfter) > 0 {
		fmt.Fprintf(&b, "Certs on device now: %s\n", strings.Join(r.CertsAfter, ", "))
	} else if r.CertsUploaded || len(r.CertsDeleted) > 0 {
		b.WriteString("Certs on device now: (none)\n")
	}
	if len(r.VerifyMismatches) > 0 {
		b.WriteString("Verify mismatches:\n")
		for _, m := range r.VerifyMismatches {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// IdentifyLabel builds a human device label from cfg_info.
func IdentifyLabel(info DeviceInfo) string {
	parts := make([]string, 0, 3)
	if info.IMEI != "" {
		parts = append(parts, "IMEI "+info.IMEI)
	}
	if info.HWFamily != "" {
		hw := info.HWFamily
		if info.HWVariant != "" {
			hw += ":" + info.HWVariant
		}
		parts = append(parts, hw)
	}
	if fw := info.FWFull(); fw != "" {
		parts = append(parts, "FW "+fw)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " / ")
}

// FormatCfgInfoVerbose returns a multi-line dump of all cfg_info fields for -v.
func FormatCfgInfoVerbose(info DeviceInfo) string {
	if len(info.Raw) == 0 {
		return "cfg_info: (empty)"
	}
	keys := make([]int, 0, len(info.Raw))
	for k := range info.Raw {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var b strings.Builder
	b.WriteString("cfg_info (:cfg_info:?):")
	for _, k := range keys {
		label := cfgInfoFieldLabel(k)
		if label != "" {
			fmt.Fprintf(&b, "\n  %d %s: %s", k, label, info.Raw[k])
		} else {
			fmt.Fprintf(&b, "\n  %d: %s", k, info.Raw[k])
		}
	}
	return b.String()
}

func cfgInfoFieldLabel(n int) string {
	switch n {
	case 0:
		return "firmware"
	case 1:
		return "config/protocol"
	case 2:
		return "hardware"
	case 3:
		return "IMEI"
	case 5:
		return "init"
	case 6:
		return "SIM"
	case 8:
		return "keyword (0=set/may lock, 1=none)"
	case 12:
		return "build"
	case 13:
		return "firmware revision"
	case 14:
		return "modem/module"
	default:
		return ""
	}
}

// ParseGetCfgParams parses a :cfg_getcfg response into id -> value.
func ParseGetCfgParams(text string) map[string]string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	out := make(map[string]string)
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "<") || strings.HasPrefix(ln, "[") {
			continue
		}
		id, val, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		allDigits := true
		for _, r := range id {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits {
			continue
		}
		out[id] = strings.TrimSpace(val)
	}
	return out
}

// valuesMatch reports equality with numeric normalization (4.1 == 4.100000).
func valuesMatch(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return true
	}
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	if aerr == nil && berr == nil {
		return af == bf
	}
	return false
}

// FilterResult is the breakdown of config params vs post-reset device defaults.
type FilterResult struct {
	Keep        []Param
	Unsupported []Param
	Unchanged   []Param
}

// FilterParamsToDevice keeps config params the device reported after reset.
// Values that already match the device default are skipped.
func FilterParamsToDevice(want []Param, deviceDefaults map[string]string) FilterResult {
	var out FilterResult
	for _, p := range want {
		def, ok := deviceDefaults[p.ID]
		if !ok {
			out.Unsupported = append(out.Unsupported, p)
			continue
		}
		if valuesMatch(p.Value, def) {
			out.Unchanged = append(out.Unchanged, p)
			continue
		}
		out.Keep = append(out.Keep, p)
	}
	return out
}

// VerifyParams checks that device values match the programmed set.
func VerifyParams(want []Param, got map[string]string) []string {
	var mismatches []string
	for _, p := range want {
		actual, ok := got[p.ID]
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("%s: missing on device (want %q)", p.ID, p.Value))
			continue
		}
		if !valuesMatch(p.Value, actual) {
			mismatches = append(mismatches, fmt.Sprintf("%s: want %q got %q", p.ID, p.Value, actual))
		}
	}
	return mismatches
}

// GetCfg runs :cfg_getcfg and returns parsed parameters.
// Full dumps are ~7k lines and often pause mid-transfer — must not use TxText's
// short idle/hard deadlines (those truncate the map and close the port).
func (c *Client) GetCfg(wait time.Duration) (map[string]string, string, error) {
	if wait <= 0 {
		wait = 45 * time.Second
	}
	if c.isClosed() {
		return nil, "", fmt.Errorf("cfg_getcfg: port closed")
	}
	c.logf("TX %q", ":cfg_getcfg")
	raw := c.txRawHard([]byte(":cfg_getcfg\r"), wait, 2*time.Second, wait+5*time.Second)
	text := string(raw)
	if c.isClosed() && !strings.Contains(text, "GET_PARAMS_END") {
		return nil, text, fmt.Errorf("cfg_getcfg: port closed before GET_PARAMS_END (dump truncated)")
	}
	if !strings.Contains(text, "GET_PARAMS_START") {
		return nil, text, fmt.Errorf("cfg_getcfg failed: %q", truncate(text, 160))
	}
	if !strings.Contains(text, "GET_PARAMS_END") {
		return nil, text, fmt.Errorf("cfg_getcfg incomplete: no GET_PARAMS_END (%d bytes, %d ids)", len(text), len(ParseGetCfgParams(text)))
	}
	params := ParseGetCfgParams(text)
	if len(params) == 0 {
		return nil, text, fmt.Errorf("cfg_getcfg returned 0 parameters")
	}
	return params, text, nil
}

// queryCfgInfo asks :cfg_info:? with retries. Right after :sec_login the device
// sometimes needs a moment before cfg_info answers again.
func (c *Client) queryCfgInfo(attempts int, verbose func(string)) (DeviceInfo, error) {
	if attempts < 1 {
		attempts = 1
	}
	var last string
	for i := 1; i <= attempts; i++ {
		if i > 1 {
			time.Sleep(time.Duration(i) * 400 * time.Millisecond)
			c.drain(200 * time.Millisecond)
		} else {
			c.drain(200 * time.Millisecond)
		}
		raw := c.TxRaw([]byte(":cfg_info:?\r"), 4*time.Second, 500*time.Millisecond)
		c.logf("TX %q (attempt %d/%d)", ":cfg_info:?", i, attempts)
		last = string(raw)
		if strings.Contains(last, "cfg_info:") {
			return ParseCfgInfo(last), nil
		}
		if verbose != nil {
			verbose(fmt.Sprintf("cfg_info attempt %d/%d: no reply (%q)", i, attempts, truncate(last, 80)))
		}
	}
	return DeviceInfo{}, fmt.Errorf("device not responding to cfg_info")
}

// Program resets to defaults, discovers accepted params via getcfg, writes
// intersecting config values, saves, optionally uploads certs, then verifies.
// Each major stage has a hard timeout (see ProgramOptions.StepTimeout).
func (c *Client) Program(opt ProgramOptions) (*ProgramResult, error) {
	result := &ProgramResult{FileParams: len(opt.Params)}
	var defaults map[string]string

	// Keyword lock can silence cfg_info / getcfg — unlock before Identifying.
	// :sec_login can lag behind :sec_status (AT "Command not found"); retry as boot
	// noise. Keyword prompt alone needs ≥40s so typing is not cut off by the step deadline.
	kwTimeout := 40 * time.Second
	opt.progress("Checking keyword lock")
	if err := c.withStep("Checking keyword lock", opt.stepTimeout(kwTimeout), func() error {
		c.drain(400 * time.Millisecond)
		time.Sleep(500 * time.Millisecond)
		return c.EnsureKeywordUnlocked(opt.Progress, opt.Verbose, opt.PromptKeyword)
	}); err != nil {
		return result, err
	}

	opt.progress("Identifying")
	if err := c.withStep("Identifying", opt.stepTimeout(45*time.Second), func() error {
		info, err := c.queryCfgInfo(5, opt.Verbose)
		if err != nil {
			return err
		}
		// If revision missing (truncated reply), probe field 13 directly.
		if info.FWRev == "" {
			c.logf("TX %q", ":cfg_info:13")
			revText := string(c.TxRaw([]byte(":cfg_info:13\r"), 2*time.Second, 400*time.Millisecond))
			if extra := ParseCfgInfo(revText); extra.Raw[13] != "" {
				info.Raw[13] = extra.Raw[13]
				info.FWRev = extra.Raw[13]
			}
		}
		result.Info = info
		if label := IdentifyLabel(info); label != "" {
			opt.progress("Device Identified : " + label)
		}
		if info.MayBeKeywordLocked() {
			opt.progress("Configurator keyword is set on this device (session unlocked)")
		}
		opt.verbose(FormatCfgInfoVerbose(info))
		// With -v, also probe individual indices (Configurator normally only uses :?).
		if opt.Verbose != nil {
			var extra []string
			for i := 0; i <= 20; i++ {
				if _, ok := info.Raw[i]; ok {
					continue
				}
				c.logf("TX %q", fmt.Sprintf(":cfg_info:%d", i))
				reply := string(c.TxRaw([]byte(fmt.Sprintf(":cfg_info:%d\r", i)), time.Second, 300*time.Millisecond))
				parsed := ParseCfgInfo(reply)
				if v, ok := parsed.Raw[i]; ok && v != "" {
					info.Raw[i] = v
					if i == 8 {
						info.KeywordConfigured = v == "0"
					}
					extra = append(extra, fmt.Sprintf("%d=%s", i, v))
				}
			}
			if len(extra) > 0 {
				opt.verbose("cfg_info per-index extras: " + strings.Join(extra, ", "))
				result.Info = info
			} else {
				opt.verbose("cfg_info per-index probes 0–20: no fields beyond :cfg_info:? dump")
			}
		}
		return nil
	}); err != nil {
		return result, err
	}

	opt.progress("Connecting")
	if err := c.withStep("Connecting", opt.stepTimeout(30*time.Second), func() error {
		return c.cfgConnect(4)
	}); err != nil {
		return result, err
	}

	if !opt.SkipReset {
		opt.progress("Resetting")
		if err := c.withStep("Resetting", opt.stepTimeout(45*time.Second), func() error {
			opt.progress("Waiting for Reset")
			def := c.TxText(":cfg_default", 5*time.Second)
			if !strings.Contains(def, "DEFAULT_CFG_END") {
				return fmt.Errorf("reset failed: no DEFAULT_CFG_END (%q)", truncate(def, 120))
			}
			save := c.TxText(":cfg_save", 10*time.Second)
			if !strings.Contains(save, "SAVE_CFG_RESULT") {
				return fmt.Errorf("save after reset failed: %q", truncate(save, 120))
			}
			_ = c.TxText(":cfg_connect", 1500*time.Millisecond)
			return nil
		}); err != nil {
			return result, err
		}
	}

	opt.progress("Checking Defaults")
	if err := c.withStep("Checking Defaults", opt.stepTimeout(90*time.Second), func() error {
		got, raw, err := c.GetCfg(45 * time.Second)
		if err != nil {
			return fmt.Errorf("checking defaults: %w", err)
		}
		defaults = got
		result.DeviceParams = len(defaults)
		c.logf("device reported %d parameters", len(defaults))
		opt.verbose(fmt.Sprintf("Device reported %d parameters after reset", len(defaults)))
		if len(defaults) == 0 {
			if result.Info.MayBeKeywordLocked() {
				return fmt.Errorf("device returned 0 parameters (cfg_info:8=0): Configurator keyword still blocking getcfg after unlock attempt (getcfg: %q)", truncate(raw, 80))
			}
			return fmt.Errorf("device returned 0 parameters from cfg_getcfg (%q)", truncate(raw, 80))
		}
		// Successful getcfg proves the session is usable even if a keyword is configured.
		if result.Info.MayBeKeywordLocked() {
			opt.progress("Keyword configured (cfg_info:8=0) but getcfg OK — continuing")
		}
		return nil
	}); err != nil {
		return result, err
	}

	filtered := FilterParamsToDevice(opt.Params, defaults)
	result.Programmed = filtered.Keep
	result.Unsupported = filtered.Unsupported
	result.Unchanged = filtered.Unchanged
	opt.verbose(fmt.Sprintf("Filter: program %d, skip default %d, unsupported %d",
		len(filtered.Keep), len(filtered.Unchanged), len(filtered.Unsupported)))

	opt.progress(fmt.Sprintf("Programming %d/%d Parameters (%d already match, %d not on device)",
		len(filtered.Keep), len(opt.Params), len(filtered.Unchanged), len(filtered.Unsupported)))

	// After a factory reset, a non-default site config must differ somewhere.
	// Keep==0 almost always means getcfg was truncated/stale or IDs failed to parse.
	if !opt.SkipReset && len(opt.Params) > 0 && len(filtered.Keep) == 0 {
		return result, fmt.Errorf("after factory reset nothing to program (%d already match, %d not on device of %d) — getcfg likely incomplete or config IDs unrecognized",
			len(filtered.Unchanged), len(filtered.Unsupported), len(opt.Params))
	}

	if len(filtered.Keep) > 0 {
		batches := batchSetparams(filtered.Keep, 1000)
		result.Batches = len(batches)
		progTO := opt.stepTimeout(time.Duration(len(batches)+1) * 15 * time.Second)
		if err := c.withStep("Programming", progTO, func() error {
			for i, batch := range batches {
				cmd := ":cfg_setparam:" + batch + "\r"
				c.logf("batch %d/%d (%d bytes)", i+1, len(batches), len(cmd))
				opt.verbose(fmt.Sprintf("Setparam batch %d/%d (%d bytes)", i+1, len(batches), len(cmd)))
				resp := c.TxRaw([]byte(cmd), 3*time.Second, 350*time.Millisecond)
				text := string(resp)
				if !strings.Contains(text, "SETPARAM_RESULT") {
					return fmt.Errorf("programming failed on batch %d/%d: %q", i+1, len(batches), truncate(text, 160))
				}
			}
			return nil
		}); err != nil {
			return result, err
		}
	}

	opt.progress("Saving")
	if err := c.withStep("Saving", opt.stepTimeout(30*time.Second), func() error {
		save := c.TxText(":cfg_save", 10*time.Second)
		if !strings.Contains(save, "SAVE_CFG_RESULT") {
			return fmt.Errorf("save failed: %q", truncate(save, 120))
		}
		return nil
	}); err != nil {
		return result, err
	}

	wantUpload := len(opt.Root) > 0 || len(opt.Key) > 0 || len(opt.Cert) > 0
	if wantUpload {
		if len(opt.Root) == 0 || len(opt.Key) == 0 || len(opt.Cert) == 0 {
			return result, fmt.Errorf("cert upload requires all of --root, --key, and --cert")
		}
	}
	// Only delete PEMs when explicitly requested (--clear-certs) or when uploading replacements.
	wantClear := opt.ClearCerts || wantUpload
	if wantClear {
		opt.progress("Clearing certs")
		if err := c.withStep("Clearing certs", opt.stepTimeout(30*time.Second), func() error {
			deleted, err := c.DeleteCerts()
			result.CertsDeleted = append(result.CertsDeleted, deleted...)
			return err
		}); err != nil {
			return result, fmt.Errorf("clearing certs: %w", err)
		}
	}
	if wantUpload {
		opt.progress("Uploading certs")
		if err := c.withStep("Uploading certs", opt.stepTimeout(90*time.Second), func() error {
			if err := c.uploadPEM(opt.Root, CertSlotRoot, "root"); err != nil {
				return fmt.Errorf("uploading root cert: %w", err)
			}
			if err := c.uploadPEM(opt.Key, CertSlotPrivate, "private key"); err != nil {
				return fmt.Errorf("uploading private key: %w", err)
			}
			if err := c.uploadPEM(opt.Cert, CertSlotDevice, "device cert"); err != nil {
				return fmt.Errorf("uploading device cert: %w", err)
			}
			result.CertsUploaded = true
			result.CertsAfter = c.ListCertPaths()
			return nil
		}); err != nil {
			return result, err
		}
	} else if wantClear {
		result.CertsAfter = c.ListCertPaths()
	}

	opt.progress("Verifying")
	if err := c.withStep("Verifying", opt.stepTimeout(90*time.Second), func() error {
		_ = c.TxText(":cfg_connect", 1500*time.Millisecond)
		got, _, err := c.GetCfg(45 * time.Second)
		if err != nil {
			return fmt.Errorf("verify read failed: %w", err)
		}
		var toVerify []Param
		for _, p := range opt.Params {
			if _, ok := defaults[p.ID]; ok {
				toVerify = append(toVerify, p)
			}
		}
		result.VerifyMismatches = VerifyParams(toVerify, got)
		_ = c.TxText(":cfg_disconnect", time.Second)
		if len(result.VerifyMismatches) > 0 {
			n := len(result.VerifyMismatches)
			sample := result.VerifyMismatches[0]
			if n > 3 {
				return fmt.Errorf("verify failed: %d mismatch(es), e.g. %s", n, sample)
			}
			return fmt.Errorf("verify failed: %s", strings.Join(result.VerifyMismatches, "; "))
		}
		return nil
	}); err != nil {
		return result, err
	}

	if !opt.SkipReboot {
		opt.progress("Rebooting")
		if err := c.withStep("Rebooting", opt.stepTimeout(10*time.Second), func() error {
			if c.isClosed() {
				return fmt.Errorf("port closed before reboot")
			}
			c.drain(100 * time.Millisecond)
			// Configurator "Reboot device" wire command (Teltonika.Configurator): ".reset\r"
			// Device usually drops USB with little/no reply — empty is success.
			_ = c.TxText(".reset", 1500*time.Millisecond)
			opt.progress("Reboot sent — USB will drop")
			return nil
		}); err != nil {
			return result, err
		}
	}
	return result, nil
}

// certListProbe is the Configurator list-id for a cert slot (pcap: root=2, device=3, private=4).
func certListProbe(slot int) byte {
	return byte(slot + 2)
}

// certListed reports whether an FMBX 0x29 list reply includes a stored cert path.
func certListed(resp []byte) bool {
	return extractCertPath(resp) != ""
}

// extractCertPath pulls z:\cert\... from an FMBX list/delete reply.
// Paths are TLV-encoded as 00 09 10 <len> <path>; do not scan printable
// bytes past <len> — the following FMBX CRC is often printable ASCII
// (e.g. private.pem.key + CRC 0x5847 → "...keyXG").
func extractCertPath(resp []byte) string {
	for i := 0; i+4 <= len(resp); i++ {
		if resp[i] != 0x00 || resp[i+1] != 0x09 || resp[i+2] != 0x10 {
			continue
		}
		n := int(resp[i+3])
		start := i + 4
		if n <= 0 || start+n > len(resp) {
			continue
		}
		p := string(resp[start : start+n])
		if strings.HasPrefix(p, `z:\cert\`) {
			return p
		}
	}
	// Fallback for odd replies: take only path-safe characters.
	i := bytes.Index(resp, []byte(`z:\cert\`))
	if i < 0 {
		return ""
	}
	j := i
	for j < len(resp) {
		b := resp[j]
		ok := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '\\' || b == ':' || b == '.' || b == '_' || b == '-'
		if !ok {
			break
		}
		j++
	}
	return string(resp[i:j])
}

// ListCertPaths returns device-stored TLS PEM paths (Configurator Security list).
func (c *Client) ListCertPaths() []string {
	c.TxText(":sec_status", time.Second)
	var paths []string
	for _, probe := range []byte{2, 3, 4} {
		resp := c.TxFMBX(0x0028, []byte{0x02, 0x00, 0x07, 0x00, probe}, 800*time.Millisecond)
		if p := extractCertPath(resp); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// DeleteCerts removes every listed TLS PEM (DeleteCerts.pcap / Configurator Security delete).
// Returns the paths that were deleted.
func (c *Client) DeleteCerts() ([]string, error) {
	paths := c.ListCertPaths()
	if len(paths) == 0 {
		c.logf("no certs present")
		return nil, nil
	}
	for _, path := range paths {
		c.logf("=== delete cert %s ===", path)
		ack := c.TxFMBX(0x0028, CertDeletePayload(path), time.Second)
		if !bytes.Contains(ack, []byte("FMBX")) {
			return paths, fmt.Errorf("delete %s: no ACK", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if left := c.ListCertPaths(); len(left) > 0 {
		return paths, fmt.Errorf("certs still present after delete: %v", left)
	}
	return paths, nil
}

func (c *Client) uploadPEM(pem []byte, slot int, label string) error {
	c.logf("=== upload %s (%d bytes, slot %d) ===", label, len(pem), slot)
	c.TxText(":sec_status", time.Second)
	ack := c.TxFMBX(0x0028, CertSetupPayload(pem, slot), time.Second)
	if !bytes.Contains(ack, []byte("FMBX")) {
		return fmt.Errorf("%s upload: no setup ACK", label)
	}
	chunk := uint32(1)
	for off := 0; off < len(pem); off += 512 {
		end := off + 512
		if end > len(pem) {
			end = len(pem)
		}
		payload := make([]byte, 4+end-off)
		binary.BigEndian.PutUint32(payload[0:4], chunk)
		copy(payload[4:], pem[off:end])
		// Capture shows 0x2A data chunks are fire-and-forget (no FMBX reply).
		frame := FMBX(c.seq, 0x002A, payload)
		c.logf("TX FMBX data seq=%d chunk=%d data=%d", c.seq, chunk, end-off)
		_ = c.TxRaw(frame, 200*time.Millisecond, 80*time.Millisecond)
		c.seq++
		chunk++
		time.Sleep(20 * time.Millisecond)
	}
	// Device needs a moment to commit before list probes show the path.
	time.Sleep(500 * time.Millisecond)
	c.TxText(":sec_status", time.Second)
	wantProbe := certListProbe(slot)
	var listed bool
	for _, probe := range []byte{2, 3, 4} {
		resp := c.TxFMBX(0x0028, []byte{0x02, 0x00, 0x07, 0x00, probe}, 800*time.Millisecond)
		if probe == wantProbe && certListed(resp) {
			listed = true
		}
	}
	if !listed {
		return fmt.Errorf("%s upload: device did not list cert after transfer", label)
	}
	return nil
}

func batchSetparams(params []Param, maxBytes int) []string {
	var batches []string
	var cur strings.Builder
	prefix := len(":cfg_setparam:")
	for _, p := range params {
		piece := p.ID + ":" + p.Value
		need := len(piece)
		if cur.Len() > 0 {
			need++
		}
		if cur.Len() > 0 && prefix+cur.Len()+need > maxBytes {
			batches = append(batches, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(';')
		}
		cur.WriteString(piece)
	}
	if cur.Len() > 0 {
		batches = append(batches, cur.String())
	}
	return batches
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func firstLineContaining(text, sub string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}
