# GoClock

English | [简体中文](README.md)

A desktop reminder that lives in the Windows tray. When a reminder is due, a card slides into the bottom-right corner of the screen and closes itself after 15 seconds. It **never makes a sound**.

> Note: the application's user interface is in Chinese. This document describes it in English, but the buttons and labels you will see on screen are Chinese.

## Features

- **Stays in the tray** — one tray icon, nothing in the taskbar. When launched at login it opens no window at all.
- **Bottom-right toast** — a borderless card pinned to the corner of the work area. It stays on top but **never steals focus**, so it will not interrupt what you are typing. Closes after a 15-second countdown.
- **Dismiss with a click** — clicking anywhere on the card closes it; the cursor turns into a hand when you hover.
- **Hover to pause** — the countdown pauses while the pointer rests on the card and resumes from where it stopped, rather than restarting.
- **Several at once** — reminders that fire together stack upward, up to 4 on screen.
- **Recurrence** — once / daily / weekdays / weekly (any set of weekdays) / monthly (any set of dates, including the last day of the month) / yearly / every N minutes.
- **Run at login** — written to the current user's registry, no administrator rights needed.
- **Single instance** — launching it again wakes the running instance's window instead of adding a second tray icon.

## Usage

Run `GoClock.exe`. The first launch opens the management window and writes one sample reminder; every launch after that goes straight to the tray.

| Task | How |
|---|---|
| Open the management window | Left-click the tray icon, or run `GoClock.exe` again |
| Add / edit / delete | Buttons along the top; double-clicking a row also opens the editor |
| Enable or disable a reminder | Tick the checkbox in the leftmost column |
| Preview a toast | Select a row and click 预览弹窗 ("preview toast") |
| Run at login | Checkbox in the top-right corner, or the tray context menu |
| Dismiss a toast | Click anywhere on the card, or leave it — it closes after 15 seconds |
| Quit | Tray context menu → 退出 ("quit"). Closing the management window only hides it |

### Recurrence in detail

- **Weekly** — tick one or more weekdays.
- **Monthly** — enter dates as a list such as `1,15`. Enter `last` (shown as 月末) or `-1` for the final day of the month, which resolves to 28 or 29 in February automatically.
- Entering `31` for a monthly reminder fires only in months that have 31 days; shorter months are skipped.
- **Yearly** — pick a month and a date. Dates that only exist in leap years, such as February 29th, are scheduled correctly.
- **Every N minutes** — counts from the moment the program starts.

### Missed reminders

If the machine was asleep or switched off when a reminder was due, it still fires once on wake — as long as it is no more than 30 minutes late. Anything later than that is skipped silently, so you don't get a burst of toasts the moment you log in.

## Where data lives

Reminders are stored in `%APPDATA%\GoClock\alarms.json` as readable JSON. You can edit the file by hand; a UTF-8 BOM (which Notepad adds when saving) is tolerated. The `app.ico` next to it is a generated icon cache — delete it and it will be rebuilt on the next launch.

## Building

Requires Go 1.26+ (the version declared in `go.mod`). **No CGO**, and no C toolchain of any kind.

```powershell
.\build.ps1
```

Or by hand:

```powershell
go run ./tools/genicon                                                        # writes assets\icon.ico
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -ico assets/icon.ico -o rsrc.syso
go build -ldflags="-H windowsgui -s -w" -o GoClock.exe .
```

`rsrc.syso` supplies the Common Controls 6 manifest (without it the UI falls back to a pre-XP look) and the executable's icon. A copy is committed, so you only need to regenerate it after changing `app.manifest` or the icon.

The icon isn't a binary asset — `internal/icon` draws it in code, and both the tray icon and the executable icon come from that same drawing.

## Project layout

```
main.go                    entry point: single-instance check → wire up the app → message loop
internal/
  alarm/                   reminder model and "next occurrence" arithmetic (unit-tested)
  scheduler/               background scanning, emits fire events (unit-tested)
  storage/                 JSON persistence
  autostart/               run-at-login via the registry
  single/                  named mutex for single instance, plus a wake-up event
  icon/                    draws the clock icon, encodes it as ICO
  ui/
    app.go                 tray, management window, event wiring
    manager.go             table model behind the reminder list
    editor.go              add / edit dialog
    toast.go               the bottom-right toast (the whole card is custom-drawn)
tools/genicon/             small tool that writes assets\icon.ico
```

The UI is built on [lxn/walk](https://github.com/lxn/walk) — pure Win32 calls, no CGO.

### Why the toast is custom-drawn

The text on the card doesn't use label controls; the entire card is painted inside one custom widget. Two reasons:

- walk's `Label` wraps a subclassed STATIC control that forwards every mouse message to the tooltip and returns immediately, so a label's click event never fires. Cover the card in labels and clicking on the text won't dismiss it.
- A STATIC control reports the width of its text on a single line as its minimum size, so long content stretches the card far past its intended width.

Painting it directly makes the whole card a single control that can be clicked anywhere, lets GDI wrap the body text to the available width, and derives the height from the same measurement code the drawing uses. The card is always exactly 340 DIP wide.

When changing how the card looks, render it to a PNG off-screen and just look at it:

```powershell
$env:GOCLOCK_PREVIEW_DIR = "D:\preview-cards"
go test ./internal/ui -run TestRenderToastPreview -v
```

The preview calls the same measuring and painting functions the real toast uses, so the image matches what you'll see on screen.

### Threading model

walk requires every UI operation to happen on one fixed OS thread. Hence:

- `main` calls `runtime.LockOSThread()` before anything else.
- The scheduler holds **copies** of the reminders and hands changes to the UI thread through event callbacks, so the two sides never share mutable state.
- Background goroutines post work back to the UI thread through `safeSynchronize`, which skips windows that have already been destroyed.
- Toast countdowns are driven by one ticker in the toast manager, which only posts to the main window — the longest-lived window in the process.
