package ios

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/device"
)

var simctlStates = map[string]device.State{
	"Booted":        device.Booted,
	"Booting":       device.Booting,
	"Shutdown":      device.Shutdown,
	"Shutting Down": device.ShuttingDown,
}

// "...SimRuntime.iOS-18-2", and variants such as "...iOS-18-2-iPhone17-4".
var runtimeKey = regexp.MustCompile(`SimRuntime\.([A-Za-z]+)-(\d+)-(\d+)`)

// Before Xcode 10.1 the keys were names such as "iOS 12.1".
var legacyRuntimeKey = regexp.MustCompile(`^([A-Za-z]+) (\d+)\.(\d+)`)

type simctlEntry struct {
	UDID         string  `json:"udid"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	IsAvailable  *bool   `json:"isAvailable"`
	Availability *string `json:"availability"`
	// DataPathSize is the size of the simulator's data folder in bytes, which newer simctl reports.
	DataPathSize int64 `json:"dataPathSize"`
	// AvailabilityError says why a simulator cannot be used, such as a runtime that is gone.
	AvailabilityError string `json:"availabilityError"`
}

// Unavailable is a simulator that simctl lists but cannot run, because the runtime it was made for
// is no longer installed, as after an Xcode update. It keeps its data folder until it is deleted.
// It can be of any platform: `simctl delete unavailable` removes watchOS, tvOS and visionOS ones too.
type Unavailable struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Platform is the system it was made for: "iOS", "watchOS", "tvOS" or "visionOS", or empty when
	// simctl's runtime name is not one lazymobile knows.
	Platform  string `json:"platform,omitempty"`
	OSVersion string `json:"os_version,omitempty"`
	// Reason is what simctl says is wrong with it.
	Reason    string `json:"reason,omitempty"`
	DataBytes int64  `json:"data_bytes,omitempty"`
}

// available handles both simctl generations: modern ones have isAvailable, older ones an
// availability string.
func available(isAvailable *bool, availability *string) bool {
	if isAvailable != nil {
		return *isAvailable
	}
	return availability == nil || *availability == "(available)"
}

// listing is what one `simctl list devices --json` said.
type listing struct {
	devices []device.Device
	// unavailable are the simulators that cannot run, of every platform.
	unavailable []Unavailable
	// ids are all the simulators listed, usable or not, of every platform.
	ids map[string]bool
}

// ParseSimctl reads `simctl list devices --json`. Only iOS simulators are returned. Unavailable
// ones (a runtime that is no longer installed) and other platforms are skipped.
func ParseSimctl(text string) ([]device.Device, error) {
	l, err := parseSimctl(text)
	return l.devices, err
}

// ParseUnavailable reads the simulators `simctl list devices --json` says cannot be used, of every
// platform.
func ParseUnavailable(text string) ([]Unavailable, error) {
	l, err := parseSimctl(text)
	return l.unavailable, err
}

// platformName is how a simulator's system is written: simctl's "xrOS" is visionOS.
func platformName(name string) string {
	if name == "xrOS" {
		return "visionOS"
	}
	return name
}

func parseSimctl(text string) (listing, error) {
	var data struct {
		Devices map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil || data.Devices == nil {
		return listing{}, apperr.New(apperr.ParseFailed, "Could not read the simulator list from simctl")
	}

	runtimes := make([]string, 0, len(data.Devices))
	for runtime := range data.Devices {
		runtimes = append(runtimes, runtime)
	}
	sort.Strings(runtimes)

	l := listing{ids: map[string]bool{}}
	for _, runtime := range runtimes {
		m := runtimeKey.FindStringSubmatch(runtime)
		if m == nil {
			m = legacyRuntimeKey.FindStringSubmatch(runtime)
		}
		platform, version := "", ""
		if m != nil {
			platform, version = platformName(m[1]), m[2]+"."+m[3]
		}
		// One malformed entry must not hide the others, so each is decoded by itself.
		var raw []json.RawMessage
		if json.Unmarshal(data.Devices[runtime], &raw) != nil {
			continue
		}
		for _, item := range raw {
			var e simctlEntry
			if json.Unmarshal(item, &e) != nil || e.UDID == "" {
				continue
			}
			l.ids[e.UDID] = true
			if !available(e.IsAvailable, e.Availability) {
				// Every unavailable simulator is kept, whatever its system, since deleting the
				// unavailable ones deletes them all.
				name := e.Name
				if name == "" {
					name = e.UDID
				}
				l.unavailable = append(l.unavailable, Unavailable{
					ID: e.UDID, Name: name, Platform: platform, OSVersion: version,
					Reason: e.AvailabilityError, DataBytes: e.DataPathSize,
				})
				continue
			}
			if platform != "iOS" || e.Name == "" {
				continue
			}
			state, known := simctlStates[e.State]
			if !known {
				state = device.Unknown
			}
			l.devices = append(l.devices, device.Device{
				ID:        e.UDID,
				Name:      e.Name,
				Platform:  device.IOS,
				Kind:      device.Simulator,
				State:     state,
				OSVersion: version,
				Transport: "local",
				Source:    "simctl",
				DataBytes: e.DataPathSize,
			})
		}
	}
	return l, nil
}

var transports = map[string]string{"wired": "usb", "localNetwork": "wifi"}

// pick returns the first string found at any of the dotted paths.
func pick(entry map[string]any, paths ...string) (string, bool) {
	for _, path := range paths {
		var value any = entry
		for _, key := range strings.Split(path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = object[key]
		}
		switch v := value.(type) {
		case string:
			return v, true
		case map[string]any:
			// Xcode 27 reports versions as { "stringValue": "27.0", ... }.
			if s, ok := v["stringValue"].(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

// ParseDevicectl reads `devicectl list devices --json-output`. Xcode 27 moved the fields under
// `properties` and deprecated hardwareProperties, deviceProperties and connectionProperties.
// Both layouts are read, the new one first. Only physical iPhones and iPads are returned.
func ParseDevicectl(text string) ([]device.Device, error) {
	var data struct {
		Result *struct {
			Devices []json.RawMessage `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil || data.Result == nil || data.Result.Devices == nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the device list from devicectl")
	}

	var devices []device.Device
	for _, item := range data.Result.Devices {
		var entry map[string]any
		if json.Unmarshal(item, &entry) != nil {
			continue
		}
		udid, _ := pick(entry, "properties.hardware.udid", "hardwareProperties.udid")
		name, _ := pick(entry, "properties.state.name", "deviceProperties.name",
			"properties.hardware.marketingName", "hardwareProperties.marketingName")
		reality, _ := pick(entry, "properties.hardware.reality", "hardwareProperties.reality")
		deviceType, _ := pick(entry, "properties.hardware.deviceType", "hardwareProperties.deviceType")
		if udid == "" || name == "" || reality != "physical" || (deviceType != "iPhone" && deviceType != "iPad") {
			continue
		}

		transport, hasTransport := pick(entry, "properties.connection.transportType", "connectionProperties.transportType")
		link, hasLink := pick(entry, "properties.connection.state", "connectionProperties.tunnelState")
		pairing, _ := pick(entry, "properties.connection.pairingState", "connectionProperties.pairingState")
		developerMode, _ := pick(entry, "properties.state.developerModeStatus", "deviceProperties.developerModeStatus")
		version, _ := pick(entry, "properties.software.osVersionNumber", "deviceProperties.osVersionNumber")

		// "unavailable" means the device cannot be reached. Without a link state, a transport does.
		reachable := hasTransport
		if hasLink {
			reachable = link != "unavailable"
		}

		d := device.Device{
			ID:        udid,
			Name:      name,
			Platform:  device.IOS,
			Kind:      device.Physical,
			State:     device.Disconnected,
			OSVersion: version,
			Source:    "devicectl",
		}
		if reachable {
			// An unpaired device is attached but has not been trusted yet.
			d.State = device.Unauthorized
			if pairing == "paired" {
				d.State = device.Connected
			}
			d.Transport = transports[transport]
			if developerMode == "disabled" {
				d.Detail = "Developer Mode is off. Turn it on in Settings > Privacy & Security > Developer Mode."
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// ParseDevicectlApps reads `devicectl device info apps --json-output`, sorted by bundle id.
// devicectl itself leaves out the apps that come with iOS, and hidden and internal ones, unless it
// is asked for them, so every entry here is an app the user put on the device.
func ParseDevicectlApps(text string) ([]device.App, error) {
	var data struct {
		Result *struct {
			Apps []json.RawMessage `json:"apps"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil || data.Result == nil || data.Result.Apps == nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the app list from devicectl")
	}
	apps := []device.App{}
	for _, item := range data.Result.Apps {
		var e struct {
			BundleIdentifier string `json:"bundleIdentifier"`
			Name             string `json:"name"`
			Version          string `json:"version"`
			BundleVersion    string `json:"bundleVersion"`
		}
		// One malformed entry must not hide the others.
		if json.Unmarshal(item, &e) != nil || e.BundleIdentifier == "" {
			continue
		}
		apps = append(apps, device.App{ID: e.BundleIdentifier, Name: e.Name, Version: e.Version, Build: e.BundleVersion})
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps, nil
}

// Choice is something to pick by name, with the id the tool wants.
type Choice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Runtime is an installed iOS simulator runtime.
type Runtime struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// DeviceTypes are the phones and tablets this runtime can run, iPhones first.
	DeviceTypes []Choice `json:"device_types"`
}

type simDeviceType struct {
	Identifier    string `json:"identifier"`
	Name          string `json:"name"`
	ProductFamily string `json:"productFamily"`
}

// phonesAndTablets keeps iPhone and iPad types, iPhones first, in simctl's order otherwise.
// Older simctl has no productFamily, so the name decides.
func phonesAndTablets(list []simDeviceType) []Choice {
	var phones, tablets []Choice
	for _, t := range list {
		if t.Identifier == "" || t.Name == "" {
			continue
		}
		family := t.ProductFamily
		if family == "" {
			switch {
			case strings.HasPrefix(t.Name, "iPhone"):
				family = "iPhone"
			case strings.HasPrefix(t.Name, "iPad"):
				family = "iPad"
			}
		}
		switch family {
		case "iPhone":
			phones = append(phones, Choice{ID: t.Identifier, Name: t.Name})
		case "iPad":
			tablets = append(tablets, Choice{ID: t.Identifier, Name: t.Name})
		}
	}
	return append(phones, tablets...)
}

// ParseRuntimes reads `simctl list runtimes --json`: available iOS runtimes, newest first.
// Before Xcode 13 the list has no platform or supportedDeviceTypes; then the platform comes from
// the id and DeviceTypes stays empty.
func ParseRuntimes(text string) ([]Runtime, error) {
	var data struct {
		Runtimes []struct {
			Identifier           string          `json:"identifier"`
			Name                 string          `json:"name"`
			Version              string          `json:"version"`
			Platform             *string         `json:"platform"`
			IsAvailable          *bool           `json:"isAvailable"`
			Availability         *string         `json:"availability"`
			SupportedDeviceTypes []simDeviceType `json:"supportedDeviceTypes"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil || data.Runtimes == nil {
		return nil, apperr.New(apperr.ParseFailed, "Could not read the runtime list from simctl")
	}
	var runtimes []Runtime
	for _, r := range data.Runtimes {
		isIOS := r.Platform != nil && *r.Platform == "iOS" ||
			r.Platform == nil && strings.Contains(r.Identifier, "SimRuntime.iOS-")
		if r.Identifier == "" || r.Name == "" || !isIOS || !available(r.IsAvailable, r.Availability) {
			continue
		}
		runtimes = append(runtimes, Runtime{
			ID:          r.Identifier,
			Name:        r.Name,
			Version:     r.Version,
			DeviceTypes: phonesAndTablets(r.SupportedDeviceTypes),
		})
	}
	sort.SliceStable(runtimes, func(i, j int) bool {
		a, b := runtimes[i], runtimes[j]
		if c := compareVersions(a.Version, b.Version); c != 0 {
			return c > 0
		}
		// Plain runtimes before special builds of the same version.
		if len(a.ID) != len(b.ID) {
			return len(a.ID) < len(b.ID)
		}
		return a.ID < b.ID
	})
	return runtimes, nil
}

// ParseDeviceTypes reads `simctl list devicetypes --json`.
func ParseDeviceTypes(text string) []Choice {
	var data struct {
		DeviceTypes []simDeviceType `json:"devicetypes"`
	}
	if json.Unmarshal([]byte(text), &data) != nil {
		return nil
	}
	return phonesAndTablets(data.DeviceTypes)
}

// compareVersions compares dotted numeric versions: 1 when a is newer, -1 when older.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			if an > bn {
				return 1
			}
			return -1
		}
	}
	return 0
}

// ParseAppFolder returns the folder of an installed app (its .app bundle) from
// `devicectl device info apps --json-output`.
func ParseAppFolder(text, bundleID string) (string, error) {
	var data struct {
		Result struct {
			Apps []struct {
				BundleIdentifier string `json:"bundleIdentifier"`
				URL              string `json:"url"`
			} `json:"apps"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return "", apperr.New(apperr.ParseFailed, "Could not read the app list from devicectl")
	}
	for _, a := range data.Result.Apps {
		if a.BundleIdentifier == bundleID && a.URL != "" {
			return a.URL, nil
		}
	}
	return "", apperr.New(apperr.NotFound, "%s is not installed on this device", bundleID)
}

// ParseRunningExecutable finds the running process that is the app itself, not one of its
// extensions, in `devicectl device info processes --json-output`, and returns its name.
func ParseRunningExecutable(text, appFolder string) (string, bool) {
	var data struct {
		Result struct {
			Processes []struct {
				Executable string `json:"executable"`
			} `json:"runningProcesses"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(text), &data) != nil {
		return "", false
	}
	// The app list escapes spaces in a folder name ("%20"), the process list does not.
	unescape := func(s string) string {
		if u, err := url.PathUnescape(s); err == nil {
			return u
		}
		return s
	}
	folder := unescape(strings.TrimSuffix(appFolder, "/")) + "/"
	for _, p := range data.Result.Processes {
		rest, ok := strings.CutPrefix(unescape(p.Executable), folder)
		if ok && rest != "" && !strings.Contains(rest, "/") {
			return rest, true
		}
	}
	return "", false
}
