package ios

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
)

// xcodeTimeout bounds asking xcodebuild about a project. The first time, it may resolve the
// project's Swift packages, which can take a while.
const xcodeTimeout = 3 * time.Minute

const xcodeHint = "Check that the project opens in Xcode, and that `xcodebuild -list` works in its folder."

// XcodeSchemes lists the schemes of an Xcode workspace or project with `xcodebuild -list`.
// container is ["-workspace", path] or ["-project", path].
func (c *Client) XcodeSchemes(ctx context.Context, container []string) ([]string, error) {
	argv := append([]string{"xcodebuild", "-list", "-json"}, container...)
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: xcodeTimeout})
	if err := apperr.FromResult(res, argv, xcodeHint); err != nil {
		return nil, err
	}
	schemes, ok := ParseXcodeSchemes(res.Stdout)
	if !ok {
		return nil, &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the schemes from xcodebuild", Argv: argv}
	}
	return schemes, nil
}

// ParseXcodeSchemes reads `xcodebuild -list -json`, which has the schemes under "workspace" for a
// workspace and under "project" for a project.
func ParseXcodeSchemes(out string) ([]string, bool) {
	var data struct {
		Workspace *struct{ Schemes []string } `json:"workspace"`
		Project   *struct{ Schemes []string } `json:"project"`
	}
	// xcodebuild can print warnings before the JSON.
	if i := strings.Index(out, "{"); i > 0 {
		out = out[i:]
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return nil, false
	}
	switch {
	case data.Workspace != nil:
		return data.Workspace.Schemes, true
	case data.Project != nil:
		return data.Project.Schemes, true
	}
	return nil, false
}

// XcodeBuiltApp is where xcodebuild puts the app that scheme builds in its Debug configuration for
// the simulator udid, in Xcode's usual DerivedData folder, so a build made in Xcode is reused.
func (c *Client) XcodeBuiltApp(ctx context.Context, container []string, scheme, udid string) (string, error) {
	argv := append([]string{"xcodebuild", "-showBuildSettings", "-json"}, container...)
	argv = append(argv, "-scheme", scheme, "-configuration", "Debug", "-destination", "id="+udid)
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: xcodeTimeout})
	if err := apperr.FromResult(res, argv, xcodeHint); err != nil {
		return "", err
	}
	app, ok := ParseBuiltApp(res.Stdout)
	if !ok {
		return "", apperr.New(apperr.Unsupported, "The scheme %s does not build an app", scheme).
			WithHint("Pick the scheme of the app with `lazymobile run --choose`.")
	}
	return app, nil
}

// ParseBuiltApp reads `xcodebuild -showBuildSettings -json`, a list of targets, and returns the
// path of the first one that builds an app.
func ParseBuiltApp(out string) (string, bool) {
	var targets []struct {
		BuildSettings map[string]string `json:"buildSettings"`
	}
	if i := strings.Index(out, "["); i > 0 {
		out = out[i:]
	}
	if err := json.Unmarshal([]byte(out), &targets); err != nil {
		return "", false
	}
	for _, t := range targets {
		s := t.BuildSettings
		if s["WRAPPER_EXTENSION"] == "app" && s["BUILT_PRODUCTS_DIR"] != "" && s["FULL_PRODUCT_NAME"] != "" {
			return filepath.Join(s["BUILT_PRODUCTS_DIR"], s["FULL_PRODUCT_NAME"]), true
		}
	}
	return "", false
}

// BundleID is the bundle id of a built .app, from its Info.plist.
func (c *Client) BundleID(ctx context.Context, appPath string) (string, error) {
	argv := []string{"plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(appPath, "Info.plist")}
	res := c.Runner.Run(ctx, argv, run.Options{Timeout: actionTimeout})
	id := strings.TrimSpace(res.Stdout)
	if apperr.FromResult(res, argv, "") != nil || id == "" {
		return "", &apperr.Error{Code: apperr.ParseFailed, Message: "Could not read the bundle id of " + appPath, Argv: argv}
	}
	return id, nil
}
