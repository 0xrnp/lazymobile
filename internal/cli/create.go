package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/0xrnp/lazymobile/internal/android"
	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/ios"
)

// create makes a simulator or an emulator, as the dashboard's n key does.
func (a *App) create(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	list := fs.Bool("list", false, "")
	deviceType := fs.String("type", "", "")
	runtimeName := fs.String("runtime", "", "")
	image := fs.String("image", "", "")
	profile := fs.String("profile", "", "")
	positional, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *list {
		if len(positional) > 0 {
			return usageError{"create --list takes no arguments"}
		}
		return a.createList(ctx, *asJSON)
	}
	if len(positional) != 2 {
		return usageError{"create needs ios or android, and a name"}
	}
	platform, name := positional[0], positional[1]
	switch platform {
	case "ios":
		if *image != "" || *profile != "" {
			return usageError{"--image and --profile are for android"}
		}
		return a.createSimulator(ctx, name, *deviceType, *runtimeName, *asJSON)
	case "android":
		if *deviceType != "" || *runtimeName != "" {
			return usageError{"--type and --runtime are for ios"}
		}
		return a.createEmulator(ctx, name, *image, *profile, *asJSON)
	}
	return usageError{"create needs ios or android, not " + platform}
}

func (a *App) createList(ctx context.Context, asJSON bool) error {
	var runtimes []ios.Runtime
	var iosErr error
	if a.Manager.HasIOS() {
		runtimes, iosErr = a.Manager.Runtimes(ctx)
	}
	images := a.Manager.SystemImages()
	profiles, profilesErr := a.Manager.DeviceProfiles(ctx)
	if asJSON {
		if runtimes == nil {
			runtimes = []ios.Runtime{}
		}
		if images == nil {
			images = []android.SystemImage{}
		}
		if profiles == nil {
			profiles = []string{}
		}
		return a.writeJSON(map[string]any{"ios_runtimes": runtimes, "android_images": images, "android_profiles": profiles})
	}
	if iosErr != nil {
		fmt.Fprintf(a.Stderr, "warning: the iOS runtimes could not be listed: %s\n", firstLine(iosErr.Error()))
	}
	if profilesErr != nil {
		fmt.Fprintf(a.Stderr, "warning: the Android hardware profiles could not be listed: %s\n", firstLine(profilesErr.Error()))
	}
	for i, r := range runtimes {
		note := ""
		if i == 0 {
			note = "  (the newest, used by default)"
		}
		fmt.Fprintf(a.Stdout, "iOS runtime %s (%s)%s\n", r.Name, runtimeSuffix(r.ID), note)
		for _, t := range r.DeviceTypes {
			fmt.Fprintf(a.Stdout, "    type  %s\n", t.Name)
		}
	}
	for i, img := range images {
		note := ""
		if i == 0 {
			note = "  (the newest, used by default)"
		}
		fmt.Fprintf(a.Stdout, "Android image %s%s\n    %s\n", img.Label, note, img.ID)
	}
	if len(profiles) > 0 {
		fmt.Fprintf(a.Stdout, "Android profiles: %s\n", strings.Join(profiles, ", "))
	}
	if len(runtimes) == 0 && len(images) == 0 {
		fmt.Fprintln(a.Stdout, "Nothing to create from: no iOS runtime and no Android system image is installed")
	}
	return nil
}

func (a *App) createSimulator(ctx context.Context, name, wantType, wantRuntime string, asJSON bool) error {
	if !a.Manager.HasIOS() {
		return apperr.New(apperr.Unsupported, "iOS simulators need macOS")
	}
	if wantType == "" {
		return usageError{"create ios needs --type, such as --type \"iPhone 17\". `lazymobile create --list` shows them"}
	}
	runtimes, err := a.Manager.Runtimes(ctx)
	if err != nil {
		return err
	}
	if len(runtimes) == 0 {
		return apperr.New(apperr.NotFound, "No iOS runtime is installed").WithHint("Install one in Xcode, Settings, Components.")
	}
	runtime := runtimes[0]
	if wantRuntime != "" {
		names := make([]choice, len(runtimes))
		for i, r := range runtimes {
			names[i] = choice{id: r.ID, labels: []string{r.Name + " (" + runtimeSuffix(r.ID) + ")", r.Name, r.Version}}
		}
		found, err := pick("runtime", wantRuntime, names)
		if err != nil {
			return err
		}
		for _, r := range runtimes {
			if r.ID == found.id {
				runtime = r
			}
		}
	}
	types := make([]choice, len(runtime.DeviceTypes))
	for i, t := range runtime.DeviceTypes {
		types[i] = choice{id: t.ID, labels: []string{t.Name}}
	}
	chosen, err := pick("device type for "+runtime.Name, wantType, types)
	if err != nil {
		return err
	}
	id, err := a.Manager.NewSimulator(ctx, name, chosen.id, runtime.ID)
	if err != nil {
		return err
	}
	if asJSON {
		return a.writeJSON(map[string]any{"ok": true, "action": "create", "platform": "ios", "name": name, "id": id,
			"runtime": runtime.Name, "device_type": chosen.labels[0]})
	}
	fmt.Fprintf(a.Stdout, "Created the simulator %s (%s, %s). Boot it with: lazymobile boot %q\n", name, chosen.labels[0], runtime.Name, name)
	return nil
}

func (a *App) createEmulator(ctx context.Context, name, wantImage, profile string, asJSON bool) error {
	images := a.Manager.SystemImages()
	if len(images) == 0 {
		return apperr.New(apperr.NotFound, "No Android system image is installed").
			WithHint(`Install one with: sdkmanager "system-images;android-36;google_apis_playstore;arm64-v8a"`)
	}
	image := images[0]
	if wantImage != "" {
		options := make([]choice, len(images))
		for i, img := range images {
			options[i] = choice{id: img.ID, labels: []string{img.ID, img.Label, "android-" + img.API}}
		}
		found, err := pick("system image", wantImage, options)
		if err != nil {
			return err
		}
		for _, img := range images {
			if img.ID == found.id {
				image = img
			}
		}
	}
	if err := a.Manager.CreateAVD(ctx, name, image.ID, profile); err != nil {
		return err
	}
	if asJSON {
		return a.writeJSON(map[string]any{"ok": true, "action": "create", "platform": "android", "name": name, "id": name, "image": image.ID, "profile": profile})
	}
	fmt.Fprintf(a.Stdout, "Created the emulator %s (%s). Boot it with: lazymobile boot %q\n", name, image.Label, name)
	return nil
}

// runtimeSuffix tells runtimes of the same name apart: "iOS-27-2", or "iOS-27-2-iPhone19-4" for a
// special build.
func runtimeSuffix(id string) string {
	return strings.TrimPrefix(id, "com.apple.CoreSimulator.SimRuntime.")
}

// choice is something create can be asked for by any of its labels.
type choice struct {
	id     string
	labels []string
}

// pick finds the one choice that want names: an id or a label exactly, in any case, or else a part
// of exactly one choice's labels. It lists the options when there is none or several.
func pick(what, want string, options []choice) (choice, error) {
	lower := strings.ToLower(strings.TrimSpace(want))
	var partial []choice
	for _, o := range options {
		if strings.ToLower(o.id) == lower {
			return o, nil
		}
		matched := false
		for _, l := range o.labels {
			if strings.ToLower(l) == lower {
				return o, nil
			}
			matched = matched || strings.Contains(strings.ToLower(l), lower)
		}
		if matched {
			partial = append(partial, o)
		}
	}
	if len(partial) == 1 && lower != "" {
		return partial[0], nil
	}
	var names []string
	for _, o := range options {
		names = append(names, o.labels[0])
	}
	problem := "No %s matches %q"
	if len(partial) > 1 {
		problem = "More than one %s matches %q"
		names = names[:0]
		for _, o := range partial {
			names = append(names, o.labels[0])
		}
	}
	const shown = 8
	more := ""
	if len(names) > shown {
		more = fmt.Sprintf(", and %d more (`lazymobile create --list` shows them all)", len(names)-shown)
		names = names[:shown]
	}
	return choice{}, usageError{fmt.Sprintf(problem, what, want) + ". Choose one of: " + strings.Join(names, ", ") + more}
}
