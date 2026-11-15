package ios

import "encoding/json"

// Fake but realistic tool output. The structures mirror what simctl and devicectl print,
// including the Xcode 27 devicectl layout change. Every name and id here is made up.

const runtimePrefix = "com.apple.CoreSimulator.SimRuntime."

const (
	udidShutdown = "00000000-0000-4000-8000-0000000000A1"
	udidBooted   = "00000000-0000-4000-8000-0000000000A2"
)

type object = map[string]any

func sim(udid, name, state string) object {
	return object{"udid": udid, "name": name, "state": state, "isAvailable": true}
}

func encode(value any) string {
	out, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func simctlFixture() string {
	orphan := sim("00000000-0000-4000-8000-0000000000D1", "Orphan", "Shutdown")
	orphan["isAvailable"] = false
	return encode(object{"devices": object{
		runtimePrefix + "iOS-18-2": []object{
			sim(udidShutdown, "iPhone 16 Pro", "Shutdown"),
			sim(udidBooted, "iPhone 16", "Booted"),
			sim("00000000-0000-4000-8000-0000000000A3", "iPad Pro 13-inch (M4)", "Shutting Down"),
			sim("00000000-0000-4000-8000-0000000000A4", "iPhone 16 Plus", "Booting"),
			sim("00000000-0000-4000-8000-0000000000A5", "Brand New", "Creating"),
		},
		runtimePrefix + "iOS-17-5": []object{sim("00000000-0000-4000-8000-0000000000B1", "iPhone 15", "Shutdown")},
		// A special runtime build has extra text after the version.
		runtimePrefix + "iOS-18-2-iPhone17-4": []object{sim("00000000-0000-4000-8000-0000000000C1", "iPhone 17", "Shutdown")},
		// A runtime that is no longer installed. simctl still lists its devices.
		runtimePrefix + "iOS-18-0": []object{orphan},
		// Older simctl has no isAvailable, only an availability string.
		runtimePrefix + "iOS-16-4": []any{
			object{"udid": "00000000-0000-4000-8000-0000000000E1", "name": "Old Style Available", "state": "Shutdown", "availability": "(available)"},
			object{"udid": "00000000-0000-4000-8000-0000000000E2", "name": "Old Style Unavailable", "state": "Shutdown", "availability": "(unavailable, runtime profile not found)"},
			// Damaged entries are skipped, not fatal.
			object{"name": "No udid", "state": "Shutdown", "isAvailable": true},
			object{"udid": "00000000-0000-4000-8000-0000000000E3", "state": "Shutdown", "isAvailable": true},
			"not an object",
			object{"udid": 42, "name": "Wrong type", "state": "Shutdown"},
		},
		// Before Xcode 10.1 the keys were plain names.
		"iOS 12.1":                     []object{{"udid": "00000000-0000-4000-8000-0000000000E9", "name": "Ancient", "state": "Shutdown", "availability": "(available)"}},
		runtimePrefix + "watchOS-11-2": []object{sim("00000000-0000-4000-8000-0000000000F1", "Apple Watch", "Shutdown")},
		runtimePrefix + "tvOS-18-2":    []object{sim("00000000-0000-4000-8000-0000000000F2", "Apple TV", "Shutdown")},
		runtimePrefix + "xrOS-2-2":     []object{sim("00000000-0000-4000-8000-0000000000F3", "Apple Vision Pro", "Shutdown")},
		"damaged":                      "not a list",
	}})
}

type ctl struct {
	udid, name, deviceType, reality, os, developerMode, pairing, link, transport string
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ctlDevice is one entry of `devicectl list devices`, in the Xcode 27 layout ("new"), the older
// layout ("old"), or both, which is what Xcode 27 prints.
func ctlDevice(c ctl, layout string) object {
	hardware := object{
		"udid": c.udid, "deviceType": orDefault(c.deviceType, "iPhone"),
		"reality": orDefault(c.reality, "physical"), "marketingName": "Marketing Name",
	}
	connection := object{"pairingState": orDefault(c.pairing, "paired")}
	if c.transport != "" {
		connection["transportType"] = c.transport
	}
	entry := object{"identifier": "CORE-" + c.udid}
	if layout == "new" || layout == "both" {
		newConnection := object{"state": orDefault(c.link, "disconnected")}
		for k, v := range connection {
			newConnection[k] = v
		}
		entry["properties"] = object{
			"hardware": hardware,
			// Xcode 27 reports a version object, not a string.
			"software":   object{"osVersionNumber": object{"stringValue": orDefault(c.os, "18.2"), "components": []int{18, 2, 0, 0, 0}}},
			"state":      object{"name": c.name, "developerModeStatus": orDefault(c.developerMode, "enabled")},
			"connection": newConnection,
		}
	}
	if layout == "old" || layout == "both" {
		oldConnection := object{"tunnelState": orDefault(c.link, "disconnected")}
		for k, v := range connection {
			oldConnection[k] = v
		}
		entry["hardwareProperties"] = hardware
		entry["deviceProperties"] = object{
			"name": c.name, "osVersionNumber": orDefault(c.os, "18.2"),
			"developerModeStatus": orDefault(c.developerMode, "enabled"),
		}
		entry["connectionProperties"] = oldConnection
	}
	return entry
}

// ctlApp is one entry of `devicectl device info apps`, in the shape Xcode 27 prints it.
func ctlApp(id, name, version, build string) object {
	return object{
		"appClip": false, "builtByDeveloper": true, "bundleIdentifier": id, "bundleVersion": build,
		"containerAccessible": true, "defaultApp": false, "hidden": false, "internalApp": false,
		"name": name, "removable": true, "version": version,
		"url": "file:///private/var/containers/Bundle/Application/00000000-0000-4000-8000-000000000001/Example.app/",
	}
}

// devicectlAppsFixture is out of order on purpose, and ends with an entry without a bundle id.
func devicectlAppsFixture() string {
	return encode(object{
		"info": object{"commandType": "devicectl.device.info.apps", "jsonVersion": 5, "outcome": "success"},
		"result": object{
			"apps": []object{
				ctlApp("com.example.preview", "Example Preview", "2.0.0", "1"),
				ctlApp("com.example.app.staging", "Example Staging", "1.4.2", "87"),
				{"name": "No bundle id"},
			},
			"defaultAppsIncluded": false, "hiddenAppsIncluded": false, "internalAppsIncluded": false,
			"removableAppsIncluded": true, "deviceIdentifier": "00000000-0000-4000-8000-0000000000B1",
		},
	})
}

func devicectlFixture(layout string) string {
	devices := []object{
		ctlDevice(ctl{udid: "00008101-0000000000000001", name: "Test iPhone", link: "connected", transport: "wired"}, layout),
		ctlDevice(ctl{udid: "00008101-0000000000000002", name: "Test iPad", deviceType: "iPad", os: "17.6", transport: "localNetwork"}, layout),
		// Paired once, not reachable now: no transport, link unavailable.
		ctlDevice(ctl{udid: "00008101-0000000000000003", name: "Offline iPhone", link: "unavailable"}, layout),
		ctlDevice(ctl{udid: "00008101-0000000000000004", name: "Untrusted iPhone", pairing: "unpaired", transport: "wired"}, layout),
		ctlDevice(ctl{udid: "00008101-0000000000000005", name: "No Dev Mode iPhone", developerMode: "disabled", transport: "wired"}, layout),
		// devicectl also lists simulators, other platforms and damaged entries. None of them count.
		ctlDevice(ctl{udid: "SIMULATED-1", name: "A Simulator", reality: "simulated", transport: "wired"}, layout),
		ctlDevice(ctl{udid: "WATCH-1", name: "A Watch", deviceType: "appleWatch", transport: "wired"}, layout),
		{"identifier": "NO-UDID", "properties": object{"state": object{"name": "No udid"}}},
	}
	return encode(object{
		"info":   object{"jsonVersion": 5, "outcome": "success"},
		"result": object{"devices": devices},
	})
}
