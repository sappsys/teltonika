package usb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ConfigField is one metadata or parameter entry.
type ConfigField struct {
	Key, Value string
}

// ConfigDocument is a loaded .cfg (gzip) or plain-text config.
type ConfigDocument struct {
	Meta   []ConfigField
	Params []ConfigField
}

// LoadConfig reads a Teltonika .cfg (gzip) or plain-text (.txt / .cfg.txt) file.
func LoadConfig(path string) (*ConfigDocument, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".cfg.txt"):
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return parsePlainText(data)
	default:
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return extractGzipCFG(f)
	}
}

func extractGzipCFG(r io.Reader) (*ConfigDocument, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	return parseCFGText(string(raw))
}

func parseCFGText(raw string) (*ConfigDocument, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty configuration")
	}
	doc := &ConfigDocument{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("invalid segment %q", part)
		}
		field := ConfigField{Key: key, Value: value}
		if isParamKey(key) {
			doc.Params = append(doc.Params, field)
		} else {
			doc.Meta = append(doc.Meta, field)
		}
	}
	return doc, nil
}

func parsePlainText(data []byte) (*ConfigDocument, error) {
	doc := &ConfigDocument{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key:value", lineNo)
		}
		key = strings.TrimSpace(key)
		field := ConfigField{Key: key, Value: value}
		if isParamKey(key) {
			doc.Params = append(doc.Params, field)
		} else {
			doc.Meta = append(doc.Meta, field)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return doc, nil
}

func isParamKey(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if c < '0' || c > '9' {
			return false
		}
	}
	_, err := strconv.Atoi(key)
	return err == nil
}
