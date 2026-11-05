# GoClock

English | [简体中文](README.md)

A small helper that lives in the Windows tray. It currently does two things:

- **Desktop reminders** — when a reminder is due, a card slides into the bottom-right corner of the screen and closes itself after 15 seconds. It **never makes a sound**.
- **Screenshots** — modelled on WeChat's screenshot tool: select, annotate, pin, pick colours, **scrolling capture** (long screenshots stitched while you scroll), **text recognition**, **translation** and **screen recording**.

> Note: the application's user interface is in Chinese. This document describes it in English, but the buttons and labels you will see on screen are Chinese.

## Features

### Reminders

- **Stays in the tray** — one tray icon, nothing in the taskbar. When launched at login it opens no window at all.
- **Bottom-right toast** — a borderless card pinned to the corner of the work area. It stays on top but **never steals focus**, so it will not interrupt what you are typing. Closes after a 15-second countdown.
- **Dismiss with a click** — clicking anywhere on the card closes it; the cursor turns into a hand when you hover.
- **Hover to pause** — the countdown pauses while the pointer rests on the card and resumes from where it stopped, rather than restarting.
- **Acknowledgement required** (per reminder) — when enabled, letting the card time out does not count as dealing with it: the reminder comes back every 5 minutes until you click it away.
- **Several at once** — reminders that fire together stack upward, up to 4 on screen.
- **Recurrence** — once / daily / weekdays / weekly (any set of weekdays) / monthly (any set of dates, including the last day of the month) / yearly / every N minutes.
- **Run at login** — written to the current user's registry, no administrator rights needed.
- **Single instance** — launching it again wakes the running instance's window instead of adding a second tray icon.

### Screenshots

- **Global hotkey** — `Ctrl+Alt+A` by default, changeable on the 截图设置 ("screenshot settings") tab. If another program already owns it, the tray tells you.
- **Freeze and detect** — the whole desktop (all monitors) freezes the moment you press the hotkey. Hovering highlights the window or control underneath; a single click selects it.
- **Select and fine-tune** — drag out a region, resize it with eight handles, drag inside it to move it, nudge it 1 pixel with the arrow keys (10 with Shift).
- **Annotate** — rectangle, ellipse, arrow, pen and text (IME input works), 6 colours × 3 sizes, `Ctrl+Z` to undo.
- **Redact** — mosaic or blur, either brushed on or applied to a dragged rectangle.
- **Magnifier and colour picker** — a zoomed view next to the cursor shows the pixel grid, coordinates and colour; press `C` to copy `#RRGGBB`.
- **Output** — double-click or `Enter` copies to the clipboard (a PNG flavour is included, so Office and browsers paste it losslessly); `Ctrl+S` saves to a file; optionally save automatically every time.
- **Pin to desktop** — keeps a screenshot floating on top: drag to move, scroll to zoom, double-click to close, right-click to copy or save.
- **Scrolling capture** — scroll inside the region and the frames are stitched as you go, or let the program scroll for you. Sticky headers, fixed input bars, fixed sidebars and scrollbars are all handled.
- **Text recognition** — reads the text in a selection (or a pinned image, or a long screenshot) offline, using PaddleOCR (PP-OCRv5) by default or Windows' built-in OCR; like WeChat, you **select text directly on the image** to copy or translate it.
- **Translation** — recognised text, or anything you paste, goes to Google Translate. By default Chinese becomes English and everything else becomes Chinese.
- **Screen recording** — records the selection to MP4 (H.264, no audio), with pause; frame rate and whether to draw the pointer are configurable.

## Usage

Run `GoClock.exe`. The first launch opens the management window and writes one sample reminder; every launch after that goes straight to the tray.

| Task | How |
|---|---|
| Open the management window | Left-click the tray icon, or run `GoClock.exe` again |
| Add / edit / delete | Buttons along the top; double-clicking a row also opens the editor |
| Enable or disable a reminder | Tick the checkbox in the leftmost column |
| Preview a toast | Select a row and click 预览弹窗 ("preview toast") |
| Run at login | Checkbox in the top-right corner, or the tray context menu |
| Dismiss a toast | Click anywhere on the card, or leave it — it hides after 15 seconds |
| See how many are unacknowledged | Hover the tray icon; the tooltip counts them |
| Take a screenshot | Press the screenshot hotkey (`Ctrl+Alt+A` by default), or tray context menu → 截图 ("screenshot") |
| Translate some text | Copy it, then tray context menu → 文本翻译 ("translate text"); the clipboard text is translated straight away |
| Stop a recording | 完成 ("done") on the control bar, or tray context menu → 停止录屏并保存 ("stop recording and save") |
| Change the hotkey, save folder, recording or translation options | Management window → 截图设置 ("screenshot settings") tab |
| Quit | Tray context menu → 退出 ("quit"). Closing the management window only hides it |

### Screenshot controls

| Task | How |
|---|---|
| Select a whole window / control | Hover until the blue frame appears, then click |
| Free selection | Just drag; afterwards drag the border or handles to resize, or drag inside to move |
| Start over | Drag outside the selection (before annotating), or right-click to drop the selection |
| Annotate | Pick a tool on the toolbar and drag inside the selection; the row below sets size and colour |
| Redact | Pick the mosaic tool; the row below chooses brush / rectangle and mosaic / blur |
| Recognise / translate text | 文 opens the text picker: drag across the image to select text (double-click selects a segment, Shift+click extends, Ctrl+A selects all), then copy or translate the selection; 全文 ("full text") opens all the text for editing. 译 translates all the text straight away |
| Record the screen | The red camera button on the toolbar starts recording the selection |
| Type text | With the text tool, click inside the selection; `Enter` for a new line, `Ctrl+Enter` or click elsewhere to finish, `Esc` to discard |
| Copy to clipboard | Double-click the selection, `Enter`, `Ctrl+C`, or the ✓ button |
| Save to a file | `Ctrl+S`, or the save button |
| Pick a colour | Press `C` while the magnifier is showing |
| Leave | `Esc`; right-click first drops the selection, a second right-click leaves |

### Scrolling capture

1. Select a region and click 长截图 ("long screenshot") on the toolbar.
2. The overlay disappears, a blue frame stays around the region, and a preview panel opens beside it.
3. Scroll inside the frame (wheel or scrollbar). Each time the picture settles it is appended to the long image. Or click 自动滚动 ("auto scroll"): the program moves the pointer to the middle of the region and sends wheel events, stopping by itself at the bottom; moving the mouse or pressing `Esc` stops it too.
4. Click 完成 ("done") to open the result in a preview window, where you can copy it, save it, or pin it.

Notes:

- Vertical scrolling only. Scrolling back up isn't stitched in; scroll down again and it carries on from where it was.
- If one scroll jumps too far for the two frames to overlap, that frame is dropped and the panel says you scrolled too fast — scroll back a little and go slower.
- Video or animation inside the region makes alignment harder. Some programs (certain UWP apps, remote-desktop windows) ignore synthetic wheel events; scroll those by hand.
- Long images are capped at 30,000 pixels; capture stops when the cap is reached.

### Text recognition and translation

- Recognition runs locally, so **the image never leaves the machine**. There are two engines, chosen under "Recognition engine" on the screenshot settings tab:
  - **High accuracy (default)** — the PaddleOCR PP-OCRv5 mobile models, run through ONNX Runtime. On a chat screenshot it measured a 1.2% character error rate (Windows OCR: 14%), and it keeps the spaces between words in mixed Chinese/English text. On first use it downloads about 33 MB (the ONNX Runtime library plus the detection and recognition models) to `%LOCALAPPDATA%\GoClock\ocr\`. Versions are pinned and every file is checked against a SHA256: the library comes from Microsoft's onnxruntime package on PyPI (with the Tencent Cloud mirror as a fallback), the models from the RapidOCR repository on ModelScope. The download honours the proxy setting. Once loaded the engine stays in memory and is released after 1 idle minute.
  - **Built-in** — Windows OCR: nothing to download and fast, but less accurate. The recogniser follows the system language (the Simplified Chinese recogniser also reads Latin letters and digits). For better English-only results, install the English OCR pack as administrator: `Add-WindowsCapability -Online -Name "Language.OCR~~~en-US~0.0.1.0"`
- 文 opens the **text picker**: the screenshot is shown as-is, with recognised text tinted light blue. Drag to select text, double-click to select a segment, Shift+click to extend, Ctrl+A to select all; Ctrl+C or 复制 ("copy") copies the selection (everything if nothing is selected), 翻译 ("translate") sends the selection to the translation window, 全文 ("full text") opens all the text for editing. In multi-column layouts (such as a three-pane mail client), dragging down one column selects only that column; on long images, dragging past the window edge scrolls automatically.
- If the high-accuracy engine can't be downloaded or loaded, recognition falls back to Windows OCR and the result window says why. After a failed download there are no retries for 10 minutes, so each recognition doesn't sit waiting for the network.
- Long images are recognised in tiles. The high-accuracy engine goes line by line with progress in the status bar; a full-page long screenshot of about 300 lines takes a dozen seconds or so. Windows OCR upscales by 1.5× first and takes a few seconds.
- Known weak spots of Windows OCR: one- or two-character lines and coloured bold headings are sometimes missed — fix them in the result box.
- **Translation sends the text to Google.** Without a key it uses the free, unofficial endpoint (which may be rate-limited); with an API key entered on the settings tab it uses the official Cloud Translation API. The key is encrypted with Windows DPAPI and only the current Windows account can decrypt it.
- Network access follows the system proxy (the manual proxy in Internet Options) unless you enter one on the settings tab; PAC scripts are not supported.

### Screen recording

1. Select a region and click the record button on the toolbar.
2. A red frame appears around the region, with a control bar beside it: timer, 暂停 / 继续 ("pause / resume"), 完成 ("done"), 放弃 ("discard"). The bar can be dragged out of the way.
3. On 完成 the video is saved to the screenshot folder (`GoClock_date_time.mp4`) and the tray shows a notification; clicking it opens the folder with the file selected.

- Encoding uses Windows' own Media Foundation H.264 encoder (hardware-accelerated when the GPU offers it); nothing extra to install.
- The frame and the control bar never appear in the video. The screenshot hotkey is ignored, with a notice, while recording.
- An odd width or height is trimmed by one pixel (an H.264 requirement). This version records no audio.

### Recurrence in detail

- **Weekly** — tick one or more weekdays.
- **Monthly** — enter dates as a list such as `1,15`. Enter `last` (shown as 月末) or `-1` for the final day of the month, which resolves to 28 or 29 in February automatically.
- Entering `31` for a monthly reminder fires only in months that have 31 days; shorter months are skipped.
- **Yearly** — pick a month and a date. Dates that only exist in leap years, such as February 29th, are scheduled correctly.
- **Every N minutes** — counts from the moment the program starts.

### Requiring acknowledgement

Each reminder has a 必须手动关闭 ("must be closed by hand") checkbox in the editor. The 关闭方式 ("dismissal") column in the list shows which reminders have it on.

With it enabled the card's hint changes to 点击确认 ("click to acknowledge"), because for these reminders the 15-second countdown only **hides** the card:

- You click the card → done, that occurrence is finished.
- You don't, and it fades on its own → it comes back 5 minutes later, marked 第 N 次 ("attempt N"), and keeps coming back until you click it.
- The same alarm falls due again in the meantime → the counter restarts at 1 rather than piling up a second entry.

Disabling the alarm, deleting it, or unticking the box drops any outstanding acknowledgement along with it.

Outstanding acknowledgements are **not persisted** — quitting or restarting clears them, so you never get a pile of them at login.

### Missed reminders

If the machine was asleep or switched off when a reminder was due, it still fires once on wake — as long as it is no more than 30 minutes late. Anything later than that is skipped silently, so you don't get a burst of toasts the moment you log in.

## Where data lives

Reminders are stored in `%APPDATA%\GoClock\alarms.json` as readable JSON. You can edit the file by hand; a UTF-8 BOM (which Notepad adds when saving) is tolerated. Program settings (screenshot hotkey, save folder, recording and translation options and so on) live next to it in `settings.json`; the Google API key in it is encrypted. The `app.ico` in the same folder is a generated icon cache — delete it and it will be rebuilt on the next launch.

Screenshots and recordings are saved to `Pictures\GoClock` by default, with names like `GoClock_20261104_153012.png` / `.mp4`.

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
  storage/                 JSON persistence for reminders and program settings, DPAPI encryption (unit-tested)
  autostart/               run-at-login via the registry
  single/                  named mutex for single instance, plus a wake-up event
  icon/                    draws the clock icon, encodes it as ICO
  hotkey/                  global hotkeys and parsing of "Ctrl+Alt+A" (unit-tested)
  capture/                 screenshot logic with no UI in it
    screen.go              physical-pixel screen capture, multi-monitor and DPI, monitor info
    windows.go             window / control enumeration for hover detection (unit-tested)
    stitch.go              scrolling-capture stitching (unit-tested)
    clipboard.go           images to the clipboard (CF_DIB + PNG)
    save.go                PNG saving and file naming
    cursor.go              draws the mouse pointer into recorded frames
  ocr/                     text recognition (unit-tested)
    paddle.go              PaddleOCR: text detection, line recognition, CTC decoding (with per-character positions) and word spacing
    ort.go                 minimal binding for the ONNX Runtime C API
    assets.go              downloading the high-accuracy files, source fallback and SHA256 checks
    winrt.go / ocr.go      minimal WinRT binding for Windows OCR and result clean-up
    tiles.go               tiling and upscaling for Windows OCR
  translate/               Google Translate: free endpoint and official API (unit-tested)
  httpx/                   HTTP client using the configured or system proxy (unit-tested)
  record/                  Media Foundation H.264 / MP4 encoder and the recorder (unit-tested)
  ui/
    app.go                 tray, management window, event wiring
    manager.go             table model behind the reminder list
    editor.go              add / edit dialog
    toast.go               the bottom-right toast (the whole card is custom-drawn)
    pending.go             queue of reminders awaiting acknowledgement (unit-tested)
    settings_page.go       the screenshot settings tab
    shot.go                screenshot wiring: start, handle results, manage pins and scrolling capture
    shot_overlay.go        full-screen overlay: detection, selection, annotation input, magnifier
    shot_toolbar.go        toolbar and palette (icons drawn in code)
    shot_annot.go          annotation model and drawing, shared by screen and export
    shot_long.go           scrolling-capture session: frame, preview panel, manual / auto scroll
    shot_viewer.go         long-image preview window
    shot_pin.go            pinned image window
    shot_picker.go         text picker: select text on the image, copy, translate
    shot_picker_select.go  text selection logic: segments, hit testing, column-aware selection (unit-tested)
    shot_text.go           text and translation window (full text, translation)
    ocr_engine.go          on-demand download, caching, idle release and Windows OCR fallback for the high-accuracy engine
    shot_record.go         recording session: red frame and control bar
    win32wnd.go            helpers for native Win32 windows and GDI
    gdiplus.go             minimal GDI+ binding (anti-aliased annotations)
tools/genicon/             small tool that writes assets\icon.ico
```

The UI is built on [lxn/walk](https://github.com/lxn/walk) — pure Win32 calls, no CGO. The screenshot windows are the exception; see below.

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

### Why the screenshot windows don't use walk controls

The overlay, pinned images, the scrolling-capture panel and the long-image preview are native Win32 windows. They need full-screen double-buffered painting, high-rate mouse input, click-through layered windows and per-monitor DPI awareness, none of which walk's controls provide. walk's message loop calls `GetMessage(msg, 0, 0, 0)` and so dispatches every message on the thread; native windows simply live on the UI thread alongside walk's, with no extra thread.

- **DPI** — the manifest declares system DPI awareness, which makes captures on a secondary monitor with a different scale factor blurry. Screenshot windows are created, and captures taken, under a temporary per-monitor-v2 context (`capture.WithPerMonitorDPI`), so they see real physical pixels. The manifest is unchanged and the reminder UI is unaffected.
- **Anti-aliasing** — the frozen screen is blitted with GDI (fast); annotations are drawn with GDI+ (anti-aliased). The screen and the exported image use the same drawing code.
- **Our own windows stay out of the picture** — the frames and panels for scrolling capture and recording are marked `WDA_EXCLUDEFROMCAPTURE`.
- **Input methods** — with a Chinese IME active, keys such as Esc and C are swallowed by the IME, so the overlay, pinned images and the preview window run without an IME, attaching one only while you type a text annotation.
- **No third-party Go libraries and no CGO for OCR and recording** — WinRT (Windows OCR), the ONNX Runtime C API (high-accuracy OCR) and Media Foundation (encoding) are reached through small hand-written bindings that receive interface pointers via out-parameters, keeping `go vet` clean. They are covered by unit and on-machine tests (the encoder test writes a video and checks its MP4 structure). The ONNX Runtime library isn't shipped with the program; it is downloaded on first use.
- **`//go:uintptrescapes`** — when a wrapper function passes `uintptr(unsafe.Pointer(&out))` on to a system call, the wrapper needs this directive. Without it `out` stays on the stack, and once the stack grows and moves, the callee writes to the old address (this once showed up as ONNX Runtime inference succeeding while the output came back as a null pointer).

Acceptance and live tests for high-accuracy recognition (skipped by default; they need the library, models and sample images, or permission to download; the acceptance image must match the reference text `accuracyTruth` in `paddle_test.go`):

```powershell
$env:GOCLOCK_PADDLE_DIR = "D:\paddle"; $env:GOCLOCK_ACC_CHAT = "D:\chat.png"
go test ./internal/ocr -run "TestPaddleAccuracy|TestPaddleReopenAfterClose" -v   # character error rate at most 3%
$env:GOCLOCK_LIVE_DOWNLOAD = "1"; $env:GOCLOCK_OCR_IMAGE = "D:\chat.png"
go test ./internal/ocr -run TestPaddleSourcesLive -v                              # every download source, SHA256-checked
go test ./internal/ui -run TestOCR -v                                             # first-use download, idle release, fallback
```

The toolbar and annotations can be rendered off-screen to PNG too (at 100% and 150%):

```powershell
$env:GOCLOCK_PREVIEW_DIR = "D:\preview-cards"
go test ./internal/ui -run TestRenderShotPreview -v
```

### How scrolling capture stitches

The input is a series of settled captures of one region taken while it scrolls. Between two consecutive frames, fixed bars at the top and bottom stay put, fixed sidebars may sit at the sides, and the middle has moved up by dy rows. Stitching means finding dy and appending the bottom dy rows of the new frame's middle.

- Columns that changed between the frames are found first, which excludes sidebars that don't move; narrow scrollbar-like strips are then peeled off both edges.
- Each row is split into segments that are fingerprinted separately. Segment fingerprints vote for candidate offsets, each candidate is verified row by row, and the best match wins. A blinking caret or a hover highlight only dirties one segment, not the whole row.
- Plain (blank) segments carry no information and are ignored. When too few rows with content overlap, the frame is dropped rather than guessed.
- The unit tests cut synthetic pages (sticky header, footer, sidebar, scrollbar, large blank areas) into frames, stitch them back, and require a pixel-exact match with the original page.

### Threading model

walk requires every UI operation to happen on one fixed OS thread. Hence:

- `main` calls `runtime.LockOSThread()` before anything else.
- The scheduler holds **copies** of the reminders and hands changes to the UI thread through event callbacks, so the two sides never share mutable state.
- Background goroutines post work back to the UI thread through `safeSynchronize`, which skips windows that have already been destroyed.
- Toast countdowns are driven by one ticker in the toast manager, which only posts to the main window — the longest-lived window in the process.
