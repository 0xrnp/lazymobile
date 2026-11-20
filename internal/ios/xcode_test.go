package ios

import (
	"context"
	"slices"
	"testing"

	"github.com/0xrnp/lazymobile/internal/apperr"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

func TestParseXcodeSchemes(t *testing.T) {
	// `xcodebuild -list -json` from Xcode 27.2, for a project and for a CocoaPods workspace.
	project := `{"project": {"configurations": ["Debug", "Release"], "name": "HelloSwift", "schemes": ["HelloSwift"], "targets": ["HelloSwift"]}}`
	workspace := "Command line invocation:\n    xcodebuild -list\n" + `{"workspace": {"name": "Shop", "schemes": ["Pods-Shop", "Shop", "ShopTests"]}}`
	if got, ok := ParseXcodeSchemes(project); !ok || !slices.Equal(got, []string{"HelloSwift"}) {
		t.Errorf("project: %q, %v", got, ok)
	}
	if got, ok := ParseXcodeSchemes(workspace); !ok || !slices.Equal(got, []string{"Pods-Shop", "Shop", "ShopTests"}) {
		t.Errorf("workspace: %q, %v", got, ok)
	}
	if _, ok := ParseXcodeSchemes("xcodebuild: error: no project"); ok {
		t.Error("an error was read as schemes")
	}
}

// builtSettings is `xcodebuild -showBuildSettings -json` for a scheme that builds a framework and
// then the app, shortened.
const builtSettings = `[
  {"action": "build", "buildSettings": {"WRAPPER_EXTENSION": "framework", "BUILT_PRODUCTS_DIR": "/DD/Build/Products/Debug-iphonesimulator", "FULL_PRODUCT_NAME": "Kit.framework"}, "target": "Kit"},
  {"action": "build", "buildSettings": {"WRAPPER_EXTENSION": "app", "BUILT_PRODUCTS_DIR": "/DD/Build/Products/Debug-iphonesimulator", "FULL_PRODUCT_NAME": "HelloSwift.app", "PRODUCT_BUNDLE_IDENTIFIER": "dev.lazymobile.helloswift"}, "target": "HelloSwift"}
]`

func TestXcodeBuiltAppIsTheAppOfTheScheme(t *testing.T) {
	fake := runtest.Table(map[string]run.Result{"xcodebuild -showBuildSettings": runtest.Out(builtSettings)})
	app, err := New(fake).XcodeBuiltApp(context.Background(), []string{"-project", "/p/HelloSwift.xcodeproj"}, "HelloSwift", "SIM-1")
	if err != nil || app != "/DD/Build/Products/Debug-iphonesimulator/HelloSwift.app" {
		t.Errorf("%q, %v", app, err)
	}
	if !fake.Called("-scheme HelloSwift -configuration Debug -destination id=SIM-1") {
		t.Errorf("calls %q", fake.Calls())
	}
	fake = runtest.Table(map[string]run.Result{"xcodebuild -showBuildSettings": runtest.Out(`[{"buildSettings": {"WRAPPER_EXTENSION": "framework"}}]`)})
	if _, err := New(fake).XcodeBuiltApp(context.Background(), []string{"-project", "/p/Kit.xcodeproj"}, "Kit", "SIM-1"); apperr.CodeOf(err) != apperr.Unsupported {
		t.Errorf("a scheme without an app: %v", err)
	}
}
