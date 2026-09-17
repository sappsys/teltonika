package usb

import (
	"fmt"
	"strings"
)

// Curated names/values for a few well-known config IDs (local reference only).
// This experimental tool does not ship the full Tracker wiki name catalog.
var curated = map[string]struct {
	Name   string
	Values map[string]string
}{
	"101": {
		Name: "Ignition settings",
		Values: map[string]string{
			"2": "Accelerometer", "3": "Digital Input or Accelerometer", "4": "Power Voltage",
			"5": "Digital Input or Power Voltage", "6": "Accelerometer or Power Voltage",
			"7": "Digital Input, Accelerometer, or Power Voltage", "8": "Engine RPM",
			"9": "Digital Input or Engine RPM", "10": "Accelerometer or Engine RPM",
			"11": "Digital Input, Accelerometer or Engine RPM", "12": "Power Voltage or Engine RPM",
			"13": "Digital Input, Power Voltage or Engine RPM", "14": "Accelerometer, Power Voltage or Engine RPM",
		},
	},
	"138": {
		Name: "Movement Source",
		Values: map[string]string{
			"1": "Ignition", "2": "Accelerometer", "3": "Ignition+Accelerometer",
		},
	},
	"271": {
		Name: "Ignition Source Operand",
		Values: map[string]string{
			"0": "OR", "1": "AND",
		},
	},
}

// ParamHint returns a short label for summaries. unknown=true may add value enums.
func ParamHint(_fmType, id, value string, _uncertain bool) string {
	if e, ok := curated[id]; ok {
		name := e.Name
		if lab, ok := e.Values[strings.TrimSpace(value)]; ok {
			return fmt.Sprintf("%s; %s=%s", name, value, lab)
		}
		return name
	}
	return fmt.Sprintf("Parameter %s", id)
}
