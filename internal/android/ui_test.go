package android

import (
	"context"
	"strings"
	"testing"

	"github.com/0xrnp/lazymobile/internal/device"
	"github.com/0xrnp/lazymobile/internal/run"
	"github.com/0xrnp/lazymobile/internal/run/runtest"
)

const dump = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?><hierarchy rotation="0">
<node text="" resource-id="" class="android.widget.FrameLayout" package="com.example.app" content-desc="" clickable="false" enabled="true" focused="false" scrollable="false" long-clickable="false" password="false" checked="false" bounds="[0,0][1080,2400]">
 <node text="Sign in" resource-id="com.example.app:id/login" class="android.widget.Button" package="com.example.app" content-desc="" clickable="true" enabled="true" focused="false" scrollable="false" long-clickable="false" password="false" checked="false" bounds="[100,1000][500,1100]"/>
 <node text="hunter2" resource-id="com.example.app:id/pass" class="android.widget.EditText" package="com.example.app" content-desc="" clickable="true" enabled="true" focused="true" scrollable="false" long-clickable="false" password="true" checked="false" bounds="[100,800][980,900]"/>
 <node text="" resource-id="" class="android.view.View" package="com.example.app" content-desc="Menu" clickable="true" enabled="false" focused="false" scrollable="false" long-clickable="false" password="false" checked="false" bounds="[0,0][100,100]"/>
 <node text="" resource-id="" class="android.view.View" package="com.example.app" content-desc="" clickable="false" enabled="true" focused="false" scrollable="false" long-clickable="false" password="false" checked="false" bounds="[0,0][50,50]"/>
 <node text="Hidden" resource-id="" class="android.view.View" package="com.example.app" content-desc="" clickable="false" enabled="true" focused="false" scrollable="false" long-clickable="false" password="false" checked="false" bounds="[0,0][0,0]"/>
</node></hierarchy>
UI hierarchy dumped to: /dev/tty`

func TestParseScreenListsWhatCanBeReadOrTouched(t *testing.T) {
	screen, err := ParseScreen(dump)
	if err != nil {
		t.Fatal(err)
	}
	if screen.Width != 1080 || screen.Height != 2400 || screen.App != "com.example.app" {
		t.Errorf("screen %+v", screen)
	}
	// The sign in button, the password field and the menu: not the empty view or the zero-size one.
	if len(screen.Elements) != 3 {
		t.Fatalf("%d elements: %+v", len(screen.Elements), screen.Elements)
	}
	login, pass, menu := screen.Elements[0], screen.Elements[1], screen.Elements[2]
	if login.Text != "Sign in" || login.X != 300 || login.Y != 1050 || !login.Tappable || login.Class != "Button" {
		t.Errorf("login %+v", login)
	}
	// A password is never read out.
	if pass.Text != "•••••••" || !pass.Focused {
		t.Errorf("password %+v", pass)
	}
	if menu.Desc != "Menu" || !menu.Disabled {
		t.Errorf("menu %+v", menu)
	}
	if _, err := ParseScreen("ERROR: could not get idle state."); err == nil {
		t.Error("no hierarchy was accepted")
	}
}

func TestInputGoesToAnEmulatorAndTypedTextIsQuoted(t *testing.T) {
	fake := &runtest.Fake{Answer: func(string, []string, run.Options) run.Result { return run.Result{} }}
	c := New(fake, sdk(t, "adb"))
	ctx := context.Background()
	if err := c.TypeText(ctx, runningEmulator, `it's $(reboot) 100%`); err != nil {
		t.Fatal(err)
	}
	if !fake.Called(`input text 'it'\''s%s$(reboot)%s100%'`) {
		t.Errorf("calls %q", fake.Calls())
	}
	for _, bad := range []string{"", "café", "two\nlines", strings.Repeat("a", maxTyped+1)} {
		if err := c.TypeText(ctx, runningEmulator, bad); err == nil {
			t.Errorf("%q was typed", bad)
		}
	}
	if err := c.Key(ctx, runningEmulator, "Back"); err != nil || !fake.Called("input keyevent KEYCODE_BACK") {
		t.Errorf("back: %v %q", err, fake.Calls())
	}
	if err := c.Key(ctx, runningEmulator, "power"); err == nil {
		t.Error("power was pressed")
	}
	if err := c.Tap(ctx, runningEmulator, 10, 20); err != nil || !fake.Called("input tap 10 20") {
		t.Errorf("tap: %v %q", err, fake.Calls())
	}
	phone := device.Device{Platform: device.Android, Kind: device.Physical, State: device.Connected, ID: "R5CT", Serial: "R5CT"}
	if err := c.Tap(ctx, phone, 1, 1); err == nil {
		t.Error("a phone was touched")
	}
}
