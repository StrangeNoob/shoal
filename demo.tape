# demo.tape — regenerate demo.gif with:  vhs demo.tape
# Requires vhs (https://github.com/charmbracelet/vhs), which needs ttyd + ffmpeg.
#
# Drives the TUI through the splash, a search, the panes, and a live theme
# toggle, then quits. (The committed demo.gif was recorded to demo.cast and
# rendered with agg — see "Regenerating the demo" in the README.)

Output demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 640
Set Padding 12
Set Framerate 24

# Build once off-camera, then launch.
Hide
Type "go build -o /tmp/shoal ./cmd/shoal" Enter
Sleep 3s
Show

Type "/tmp/shoal" Enter
Sleep 3s          # animated splash → home screen

Type "blender"
Sleep 1s
Enter
Sleep 3500ms      # live search: "searching… N/M sources" → results table

Escape
Sleep 500ms
Tab               # Downloads
Sleep 1500ms
Tab               # Seeding (+ history)
Sleep 1500ms
Tab               # Settings
Sleep 1500ms
Right             # toggle theme Twilight → Tide (live)
Sleep 2s
Tab               # back to Search
Sleep 1s

Type "q"          # quit
Sleep 1s
