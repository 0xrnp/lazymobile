package android

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/device"
)

// Android versions by API level, for display only.
var releases = map[int]string{
	21: "5.0", 22: "5.1", 23: "6", 24: "7.0", 25: "7.1", 26: "8.0", 27: "8.1", 28: "9", 29: "10",
	30: "11", 31: "12", 32: "12L", 33: "13", 34: "14", 35: "15", 36: "16",
}

// ReleaseForAPI is "16" for API 36, and "API 99" for a level this table does not know yet.
func ReleaseForAPI(api string) string {
	level, err := strconv.Atoi(api)
	if err != nil {
		return ""
	}
	if release, ok := releases[level]; ok {
		return release
	}
	return "API " + api
}

var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidAVDName reports whether name can be an AVD name.
func ValidAVDName(name string) bool {
	return plainName.MatchString(name)
}

// ParseAVDNames reads `emulator -list-avds`. Newer emulators also print log lines such as
// "INFO    | Storing crashdata in: ..." on stdout, and AVD names cannot contain spaces.
func ParseAVDNames(text string) []string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); plainName.MatchString(line) {
			names = append(names, line)
		}
	}
	return names
}

// ADBEntry is one line of `adb devices -l`.
type ADBEntry struct {
	Serial string
	// State is adb's own word: "device", "offline", "unauthorized", "no permissions", ...
	State string
	// Props are the key:value pairs adb prints, such as "model".
	Props map[string]string
}

var adbProp = regexp.MustCompile(`(\w+):(\S+)`)

// ParseADBDevices reads `adb devices -l`.
func ParseADBDevices(text string) []ADBEntry {
	var entries []ADBEntry
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		// Skip the header, the "* daemon started" notices and blank lines.
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		serial := fields[0]
		rest := strings.TrimSpace(strings.TrimPrefix(line, serial))
		var state, after string
		if strings.HasPrefix(rest, "no permissions") {
			// "no permissions (missing udev rules? user is in the plugdev group); see [...]"
			state, after = "no permissions", strings.TrimPrefix(rest, "no permissions")
		} else {
			state, after, _ = strings.Cut(rest, " ")
		}
		props := map[string]string{}
		for _, m := range adbProp.FindAllStringSubmatch(after, -1) {
			props[m[1]] = m[2]
		}
		entries = append(entries, ADBEntry{Serial: serial, State: state, Props: props})
	}
	return entries
}

var wifiSerial = regexp.MustCompile(`^[A-Za-z0-9.-]+:\d+$`)
var emulatorSerial = regexp.MustCompile(`^emulator-\d+$`)

// IsWifiSerial reports a Wi-Fi device: "host:port", or an mDNS name such as
// "adb-XYZ._adb-tls-connect._tcp".
func IsWifiSerial(serial string) bool {
	return wifiSerial.MatchString(serial) || strings.Contains(serial, "._adb-tls-connect.")
}

// IsEmulatorSerial reports a running emulator's serial.
func IsEmulatorSerial(serial string) bool {
	return emulatorSerial.MatchString(serial)
}

var addressPattern = regexp.MustCompile(`^([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\]):\d+$`)

// ValidAddress accepts "192.168.1.5:37123" and "[fe80::1]:5555".
func ValidAddress(address string) bool {
	return addressPattern.MatchString(address)
}

var adbStates = map[string]device.State{
	"device":         device.Connected,
	"offline":        device.Offline,
	"unauthorized":   device.Unauthorized,
	"authorizing":    device.Unauthorized,
	"no permissions": device.Unauthorized,
}

func adbState(state string) device.State {
	if s, ok := adbStates[state]; ok {
		return s
	}
	return device.Unknown
}

// Fact is what a running device answered when asked.
type Fact struct {
	// AVD is the AVD a running emulator was started from.
	AVD string
	// BootKnown is true when the emulator answered the boot question; Booted is its answer.
	BootKnown bool
	Booted    bool
	Release   string
}

func emulatorState(entry ADBEntry, fact Fact) device.State {
	if entry.State != "device" {
		return adbState(entry.State)
	}
	if fact.BootKnown && !fact.Booted {
		return device.Booting
	}
	return device.Booted
}

// Combine merges AVDs and adb entries into devices. facts maps a serial to what it answered.
func Combine(names []string, entries []ADBEntry, facts map[string]Fact, info func(name string) AVDInfo) []device.Device {
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	type running struct {
		entry ADBEntry
		fact  Fact
	}
	runningByAVD := map[string]running{}
	var devices []device.Device

	for _, entry := range entries {
		fact := facts[entry.Serial]
		switch {
		case IsEmulatorSerial(entry.Serial) && fact.AVD != "" && known[fact.AVD]:
			runningByAVD[fact.AVD] = running{entry, fact}
		case IsEmulatorSerial(entry.Serial):
			// Running, but its AVD is unknown (an old emulator, or one adb could not ask).
			name := fact.AVD
			if name == "" {
				name = entry.Serial
			}
			devices = append(devices, device.Device{
				ID: entry.Serial, Name: name, Platform: device.Android, Kind: device.Emulator,
				State: emulatorState(entry, fact), OSVersion: fact.Release, Serial: entry.Serial,
				Transport: "local", Source: "adb",
			})
		default:
			d := device.Device{
				ID: entry.Serial, Name: entry.Serial, Platform: device.Android, Kind: device.Physical,
				State: adbState(entry.State), OSVersion: fact.Release, Model: entry.Props["model"],
				Serial: entry.Serial, Transport: "usb", Source: "adb",
			}
			if d.Model != "" {
				d.Name = strings.ReplaceAll(d.Model, "_", " ")
			}
			if IsWifiSerial(entry.Serial) {
				d.Transport = "wifi"
			}
			switch entry.State {
			case "unauthorized":
				d.Detail = "Unlock the phone and accept the USB debugging prompt."
			case "no permissions":
				d.Detail = "No permission to use this USB device. On Linux, add udev rules for Android devices and re-plug it."
			case "offline":
				d.Detail = "adb lost the connection. Re-plug the cable, or run: adb kill-server"
			}
			devices = append(devices, d)
		}
	}

	for _, name := range names {
		avd := info(name)
		d := device.Device{
			ID: name, Name: avd.DisplayName, Platform: device.Android, Kind: device.Emulator,
			State: device.Shutdown, OSVersion: ReleaseForAPI(avd.API), Model: avd.DeviceType,
			Transport: "local", Source: "emulator", DataBytes: avd.DataBytes,
		}
		if d.Name == "" {
			d.Name = strings.ReplaceAll(name, "_", " ")
		}
		if r, ok := runningByAVD[name]; ok {
			d.State = emulatorState(r.entry, r.fact)
			d.Serial = r.entry.Serial
		}
		devices = append(devices, d)
	}
	return devices
}

var (
	packageVersionCode = regexp.MustCompile(`(?m)^\s*versionCode=(\d+)`)
	packageVersionName = regexp.MustCompile(`(?m)^\s*versionName=([^\r\n]+)`)
)

// ParsePackageVersion reads one package's version from `dumpsys package <id>`: the name people see
// ("0.0.47") and the code that tells builds apart ("37"). A package the device does not know is
// not found. An app that updates one of the system's is listed twice, the updated copy first.
func ParsePackageVersion(text, id string) (version, build string, found bool) {
	_, record, found := strings.Cut(text, "Package ["+id+"]")
	if !found {
		return "", "", false
	}
	if m := packageVersionCode.FindStringSubmatch(record); m != nil {
		build = m[1]
	}
	if m := packageVersionName.FindStringSubmatch(record); m != nil {
		if name := strings.TrimSpace(m[1]); name != "null" {
			version = name
		}
	}
	return version, build, true
}
