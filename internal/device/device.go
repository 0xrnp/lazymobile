// Package device is the one shape every backend (simctl, devicectl, emulator, adb) reports
// devices in.
package device

import (
	"sort"
	"strings"
	"time"
)

// Platform is the operating system family of a device.
type Platform string

const (
	IOS     Platform = "ios"
	Android Platform = "android"
)

// Kind says whether a device is virtual or a real one.
type Kind string

const (
	Simulator Kind = "simulator"
	Emulator  Kind = "emulator"
	Physical  Kind = "physical"
)

// State is what a device is doing. Virtual devices are booted or shutdown. Physical devices are
// connected or disconnected. Offline and unauthorized are attached but not usable.
type State string

const (
	Booted       State = "booted"
	Booting      State = "booting"
	Shutdown     State = "shutdown"
	ShuttingDown State = "shutting_down"
	Connected    State = "connected"
	Disconnected State = "disconnected"
	Offline      State = "offline"
	Unauthorized State = "unauthorized"
	Unknown      State = "unknown"
)

// Device is one simulator, emulator or phone.
type Device struct {
	// ID is what the platform tool uses: a UDID, an AVD name, or an adb serial.
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Platform  Platform `json:"platform"`
	Kind      Kind     `json:"kind"`
	State     State    `json:"state"`
	OSVersion string   `json:"os_version,omitempty"`
	Model     string   `json:"model,omitempty"`
	// Transport is how the device is reached: "usb", "wifi" or "local".
	Transport string `json:"transport,omitempty"`
	// Serial is the adb serial of a running emulator, which links an AVD to its instance.
	Serial string `json:"serial,omitempty"`
	// Source is the tool that reported it: "simctl", "devicectl", "emulator" or "adb".
	Source string `json:"source"`
	// Detail is something the user should know, such as "Developer Mode is off".
	Detail string `json:"detail,omitempty"`
	// DataBytes is how much disk a simulator's data folder uses, or 0 when it is not known.
	DataBytes int64 `json:"data_bytes,omitempty"`
}

// Key is unique across platforms and kinds.
func (d Device) Key() string {
	return string(d.Platform) + ":" + string(d.Kind) + ":" + d.ID
}

// Ready reports whether the device can run an app right now.
func (d Device) Ready() bool {
	return d.State == Booted || d.State == Connected
}

// TargetID is the id other tools want for this device (adb -s, flutter -d, xcrun): the adb
// serial of a running emulator, otherwise the device id.
func (d Device) TargetID() string {
	if d.Platform == Android && d.Serial != "" {
		return d.Serial
	}
	return d.ID
}

// OSLabel is "iOS 18.2" or "Android 16", or "" when the version is unknown.
func (d Device) OSLabel() string {
	if d.OSVersion == "" {
		return ""
	}
	if d.Platform == IOS {
		return "iOS " + d.OSVersion
	}
	if strings.HasPrefix(d.OSVersion, "API ") {
		return d.OSVersion
	}
	return "Android " + d.OSVersion
}

// Sort orders ready devices first, then physical before virtual, then iOS before Android, then
// by name. It returns a new slice.
func Sort(list []Device) []Device {
	sorted := append([]Device(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Ready() != b.Ready() {
			return a.Ready()
		}
		if (a.Kind == Physical) != (b.Kind == Physical) {
			return a.Kind == Physical
		}
		if a.Platform != b.Platform {
			return a.Platform == IOS
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.ID < b.ID
	})
	return sorted
}

// Dedupe drops devices reported twice. The first report wins, and fields it lacks are filled in
// from later ones.
func Dedupe(list []Device) []Device {
	index := make(map[string]int, len(list))
	result := make([]Device, 0, len(list))
	for _, d := range list {
		i, seen := index[d.Key()]
		if !seen {
			index[d.Key()] = len(result)
			result = append(result, d)
			continue
		}
		first := &result[i]
		fill(&first.OSVersion, d.OSVersion)
		fill(&first.Model, d.Model)
		fill(&first.Transport, d.Transport)
		fill(&first.Serial, d.Serial)
		fill(&first.Detail, d.Detail)
	}
	return result
}

func fill(target *string, value string) {
	if *target == "" {
		*target = value
	}
}

// Match finds the devices a user means by query: an exact id or serial, an exact name, or a
// part of a name. Case does not matter. The most exact kind of match wins.
func Match(list []Device, query string) []Device {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var exactID, exactName, partial []Device
	for _, d := range list {
		name := strings.ToLower(d.Name)
		switch {
		case strings.ToLower(d.ID) == q || (d.Serial != "" && strings.ToLower(d.Serial) == q):
			exactID = append(exactID, d)
		case name == q:
			exactName = append(exactName, d)
		case strings.Contains(name, q):
			partial = append(partial, d)
		}
	}
	for _, group := range [][]Device{exactID, exactName, partial} {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

// App is an app installed on a device.
type App struct {
	// ID is the Android package name or the iOS bundle id.
	ID string `json:"id"`
	// Name is what the app is called on the home screen, when the platform says.
	Name string `json:"name,omitempty"`
	// Version is the release the user sees ("1.4.2"), and Build the number that tells builds of
	// one release apart ("87"). Either is empty when the platform does not say.
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
}

// CrashReport is the latest crash of an app, as text a person can read.
type CrashReport struct {
	App string `json:"app"`
	// When is when the crash happened, as the device wrote it.
	When string `json:"when,omitempty"`
	Text string `json:"text"`
	// Path is the full report on this computer, when there is one: an iOS simulator's crash
	// reports are files on the Mac.
	Path string `json:"path,omitempty"`
}

// VersionLabel is "1.4.2 (87)", or whichever of the two is known, or "".
func (a App) VersionLabel() string {
	switch {
	case a.Version != "" && a.Build != "":
		return a.Version + " (" + a.Build + ")"
	case a.Version != "":
		return a.Version
	case a.Build != "":
		return "(" + a.Build + ")"
	}
	return ""
}

// Usage is what an app's process has used: its CPU time since it started, and the memory it holds
// now. Two samples give its CPU use in between.
type Usage struct {
	// PID is the app's process, which the next sample can start from.
	PID int
	CPU time.Duration
	// Memory is the resident memory, in bytes.
	Memory int64
}

// Jank is how smoothly an app has drawn its frames since its counters were last reset.
type Jank struct {
	Frames int `json:"frames"`
	// Janky is how many frames missed their deadline.
	Janky int `json:"janky"`
	// Percent is Janky as a share of Frames, as the device reports it.
	Percent float64 `json:"janky_percent"`
	// P50, P90, P95 and P99 are how long frames took, in milliseconds. They are 0 when Frames is 0.
	P50 int `json:"p50_ms"`
	P90 int `json:"p90_ms"`
	P95 int `json:"p95_ms"`
	P99 int `json:"p99_ms"`
}
