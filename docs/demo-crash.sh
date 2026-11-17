#!/bin/sh
# For docs/demo.tape: waits for lazymobile to start following a log, then opens the emulator's
# Clock app and crashes it, so the demo has a crash to show.
set -e
adb shell am force-stop com.google.android.deskclock
adb logcat -c
until pgrep -f "logcat -v threadtime -T" >/dev/null; do sleep 0.3; done
sleep 1.5
adb shell monkey -p com.google.android.deskclock -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1
sleep 3.5
adb shell am crash com.google.android.deskclock
