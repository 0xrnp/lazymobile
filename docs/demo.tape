# The README demo, recorded with vhs (https://github.com/charmbracelet/vhs).
#
# Before recording:
#   - Boot a Pixel 9a emulator, and have lazymobile and adb on PATH.
#   - Set DOWN below to how many rows the emulator is below the first device in your list.
#   - In another terminal, run docs/demo-crash.sh. It waits for the log to open, then opens and
#     crashes the Clock app.
# Then, from the repository root: vhs docs/demo.tape
# Afterwards, `adb reverse --remove-all` undoes the port the demo forwards.

Output docs/demo.gif
Set Shell zsh
Set FontSize 17
Set Width 1600
Set Height 940
Set Padding 24
Set Theme "TokyoNight"
Set TypingSpeed 70ms
Set CursorBlink false
# Lower than the default, so every frame is captured at this size and the GIF plays in real time.
Set Framerate 12

Hide
Type "clear && XDG_STATE_HOME=$(mktemp -d) lazymobile"
Enter
Sleep 3s
Show

Sleep 2.5s
# DOWN
Down@110ms 12
Sleep 1s
Type "l"
Sleep 1.2s
Type@45ms "com.google.android.deskclock"
Sleep 600ms
Enter
Sleep 8s
Type "/"
Sleep 600ms
Type@70ms "AndroidRuntime"
Sleep 800ms
Enter
Sleep 2.5s
Tab
Sleep 800ms
Type "D"
Sleep 2s
Enter
Sleep 2.5s
Type "?"
Sleep 3.5s
Hide
Type "q"
