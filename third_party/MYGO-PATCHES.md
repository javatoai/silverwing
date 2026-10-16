# MyGO local native patches

## Shared TextEditor source and layout hooks

Silverwing's functional `TextEditor` wrapper reuses `ui/editor.State` and its
native buffer, selections, history, IME, find and folding. The editor patch adds
opt-in `Buffer.SetMaxSourceBytes` and `State.OnLimitExceeded`: a rejected edit
does not alter source, selection or undo. The host subtracts the UTF-8 BOM from
its source budget. `State.Composing` / composition-change notification let the
host pause its existing autosave before a preedit can be written.

`State.ReplaceContextMenu` lets document hosts replace the SDK's default Edit
menu rather than append an application menu after it. JSON and Markdown route
those commands to their existing source/document history; nil/false retains
the upstream context menu.

`NewBufferPreservingLineEndings` also treats bare CR as a displayed newline
while keeping the original rope. Generated newlines follow the first source
newline; untouched CR/LF/CRLF bytes and native clipboard values remain original.
Selection fills use a small radius, and indent guides use the same shaped-row
positions as carets and selection, including expanded tabs and font fallback.

`ui/editor/syntax_background.go` adds opt-in
`NewBackgroundTokenHighlighter`. It reuses the incremental tokenizer on a worker,
publishes only the current snapshot version and exits when idle. The application
copies language descriptors before opting in; custom highlighters retain their
own implementation. Source projections and Diff marks remain host-owned.

`State.SyntaxVisible` / `SyntaxReady` let native screenshot capture wait for
final tokens to have actually been painted for the current buffer version and
language. The queries do not start or wait for lexer jobs; clipped fields and
inactive cached owners do not block capture.

`ui/text_layout.go` adds optional `TextShape.LineHeight` for the native Markdown
rich fields' 15 DIP / 1.6 line-height layout. Zero keeps the upstream default.
Layout, caret hit testing and selection all consume the same shape.

On upgrade, compare these opt-in APIs and the editor paint paths with upstream;
remove local changes when equivalent contracts are available. Functional source
limits, original bytes and document history must remain covered by the affected
tests. SDK event tests and rendering-scale simulations do not establish physical
Windows IME candidate-window or monitor-DPI acceptance.

## Native Markdown code clipboard hook

Reading uses `markdown.View` and `DefaultCodeBlock`, including their native
layout, selection, highlighting and copy control. Optional `Options.CopyCode`
only delivers copying to the document host, which can preserve original CRLF
bytes and report clipboard failure. A nil callback retains upstream behavior;
false preserves the clipboard and clears the copied indicator. It does not
replace the native code-block renderer. Compare/remove on an upstream upgrade.

## Optional bounded toast content

`ui/toast.go` adds opt-in `Toast.ContentMaxHeight` (DIPs). Zero preserves the
upstream default rendering path. A positive value wraps the original complete
text inside a native Scroll, bounded again to the window height after reserving
toast padding and top/bottom clearance. The action remains outside the scroll.
Silverwing only opts in for its release-announcement copy-success feedback.
Toast lifetimes, hover/focus/background pauses, Escape, announcements and close
callbacks keep their upstream implementation. Compare/remove this small option
when an upstream release provides equivalent bounded toast content.

The local `mygo` module is MyGO v0.5.0 (upstream commit `208e952608a63f0d3d9cfa8c4a069b7e3a83a943`), including
its original LICENSE. `go.mod` keeps the upstream version pinned and replaces
it with this source checkout. Native runtime resources remain upstream resources.

Two files contain local dispatch changes:

- `ui/input.go`: native system Edit commands reach the focused handler first;
  a shortcut declared by the window or a focus ancestor takes priority over
  the native editor, as documented by `Element.HandleInput`.
- `ui/menu.go`: built-in text editing menus offer the command to their target
  handler before the existing selection/editor fallback.

A handler returning false keeps the upstream Edit-command fallback. Keys with
no declared shortcut keep native editing. IME and ordinary paste normalization
retain upstream behavior; diff layout extensions are documented below.
Silverwing's JSON source projection and source history handle their own commands;
other controls retain native defaults. This avoids copying an editor into the app.

On an upstream upgrade, compare these dispatch paths and remove the
local changes when upstream supports the same contract. Verification includes
source selection across folded JSON, source undo/redo through native commands,
Markdown source history through declared shortcuts and system commands, and
native fallback on an ordinary TextArea.

## Code editor clipboard failures

`ui/editor/{state,editor,edit,commands}.go` adds an optional
`State.ClipboardWrite` callback. Returning false leaves the source, selections,
undo history and previous multi-selection clipboard metadata intact. Both Cut
and CutToLineEnd check it before applying edits. A nil callback retains the
upstream services path. Silverwing's editable text-diff panes bind it to the
existing business clipboard helper and retain the exact selected source.
Keyboard, native Edit commands and the editor context menu share the same
transactions; the editor's selection/edit implementation is reused.

Compare and remove this hook when upstream exposes an equivalent error-aware
clipboard boundary. Ordinary native text fields keep their existing SDK path.

## Text-diff original line endings

`ui/editor/buffer.go` adds `NewBufferPreservingLineEndings` for Silverwing's
two editable diff panes. An additional persistent rope retains original CRLF,
LF and mixed endings in the same native edit/undo transactions. `OriginalText`
reads those bytes; regular String/Slice/Snapshot APIs retain LF display offsets.
Only affected spans are projected, including CR/LF pairs formed across an edit
boundary. The default `NewBuffer` contract remains unchanged.

`ui/editor/edit.go`, `input.go` and `commands.go` preserve raw paste payloads and copy the
original selection or newline in this mode. Carets use projected offsets.
Clipboard failure still prevents cutting. The application consumes exact
source values and keeps its existing bounded journal for oversized-input
recovery; it does not provide a second undo command implementation.
The composition path keeps its exact source byte ranges separately from the
displayed marked range. A marked LF can join an existing, unmarked CR into
one displayed CRLF; subsequent composition updates, commit and cancel replace
only their own bytes. The source tree identity invalidates stale ranges after
another edit. Targeted tests cover both boundaries, mixed endings, repeated
updates, multiple carets and undo/redo. Native OS IME acceptance remains separate
from these editor protocol tests.

Keep these changes together when upgrading upstream. Remove them only when the
native buffer can preserve mixed line endings through input, source-only
replacement, clipboard operations and native undo/redo with the same contract.

## Windows file dialog errors

`internal/windows/dialog.go` distinguishes the actual cancellation HRESULT from
creation, configuration, display and result-reading errors for Open and Save.
Multi-selection returns an error for the whole result if any selected item
cannot be read. It retains the operation name and HRESULT for the desktop's
existing error display. An unavailable optional initial folder remains a hint;
it does not prevent the user from opening the picker to choose a new location.

The adapter keeps COM result ownership and release local to each operation.
Compare this adapter on an upstream upgrade and remove the patch when upstream
provides the same behavior.

## Native diff panes and reading positions

`ui.SplitRatio` reuses the native Split's pointer capture, keyboard adjustment
and accessibility behavior while storing a width fraction; double-click resets
the fraction to one half. The original size-based Split contract is retained.
The facade in `ui/api_gen.go` exposes the corresponding core component.

`ui/editor.DiffState.SplitRatio` applies that divider inside the actual old/new
editor layout. Each side is measured at its own width before aligning wrapped
rows. Vertical scrolling remains shared; horizontal scrolling is independent.
`DiffScrollPosition` and the editor `ScrollPosition` APIs let the application
restore lightweight reading positions without retaining stale document buffers.
Editing, source bytes, undo and clipboard transactions remain editor-owned.

On upstream upgrades compare split geometry, wrapped-line alignment, scroll
clamping and position restoration before replacing these extensions. Targeted
checks cover pointer/keyboard resizing, window resizing and independent axes.

`ui/split.go` positions the ratio handle from the current layout fraction rather
than the previous frame's width. Ratio clamping requests a full layout rebuild,
including the subpixel-width case. `ui/editor/diffmodel.go` does not append an
extra LF when a hunk replaces an empty target; Accept and Revert retain one-step
undo. These fixes have dedicated first-frame, resize and empty-buffer tests.

`internal/text/caret.go` indexes existing visual stops with a boolean slice
before adding missing stops. This removes the quadratic scan on long lines;
`caret_bench_test.go` measures that isolated operation, not whole-window latency.

## Windows live resize presentation

`internal/gpu/d3d11/d3d11.go` uses `DXGI_SCALING_NONE` for HWND swap chains so
an enlarged client keeps pixel coordinates while the resize buffers have spare
capacity. Transparent spare pixels with the previous stretch policy could
appear as black margins during live resize despite correct GPU scene pixels.
DirectComposition retains its required stretch policy.

The presentation source stays at the full allocated buffer size for both
paths. Cropping an oversized HWND buffer with `SetSourceSize(sceneWidth,
sceneHeight)` while using `SCALING_NONE` magnified the visible scene by the
buffer/source ratio on the tested Windows path: at DPI 144 a fixed 60x24 px
marker became 80x32 px during a top-left drag. Keeping the full source and
clipping spare capacity at the HWND retains the scene's device-pixel viewport
and avoids a second scale. The settle test checks full source dimensions.

`internal/windows/window.go` handles `WM_SIZING` before Windows applies the
proposed outer rectangle. `surface.prepareGrowth` expands the child surface,
synchronously lays out and presents its full new frame while the parent still
clips to its old bounds, and flushes DWM before the parent exposes new pixels.
For mixed-axis changes both prepared dimensions cover the old client. The
normal `WM_SIZE` fits the exact final size; `WM_EXITSIZEMOVE` also restores it
if the proposed resize was cancelled.

`internal/gpu/d3d11/resize_policy.go` reads the owning GUI thread's
`GUITHREADINFO`, checks `GUI_INMOVESIZE` and matches `hwndMoveSize` to the
render surface's root window. Live new-size frames use `Present(0)` without
the renderer's separate DWM wait; the pre-growth surface path above waits at
the point where Windows can expose the enlarged parent. Failed state queries
retain the normal renderer waiting behavior. Buffer headroom and settling are
retained.

`MYGO_RESIZE_TRACE=1` enables opt-in child/parent size and draw/Present/DWM
timings; it is unset in normal runs. Targeted hidden-HWND tests verify resize
ordering, cancellation, transparent-root edge pixels and buffer settling.
`MYGO_RESIZE_VISUAL=1` opts into a private visible-window test that enters the
native sizing modal loop and samples the desktop during deliberately slow
frames. Its scene clears transparently and fills only its root bounds: an
opaque buffer clear would hide the spare-buffer bug. It drives only its own
window's messages and never injects global mouse input. Desktop geometry and
pixel reads are not atomic, and desktop interaction can interrupt this opt-in
test. It does not prove physical drag behavior on every display/driver.
`TestLiveResizeKeepsPixelGeometry` additionally samples fixed DIP text, an
icon and a marker over a grid during native top-left/top/left/bottom-right
growth and shrink loops, checking their pixel bounds and exposed edges before
mouse release. The first visible frame must match the known geometry, so
startup animation or a covered desktop cannot satisfy the probe.
Recheck this patch when upgrading MyGO rather than carrying it unconditionally.

## MyGO 0.5.0 Markdown reading adapters

The local native diff divider, clipboard failure handling, source-preserving
CRLF/IME transactions, caret indexing and Windows live-resize behavior extend
the v0.5.0 upstream interfaces. Keep those contracts when updating MyGO.

All shared read-only Markdown bodies use upstream `ui/markdown.View`, including
requirements and each comment, materials, task/global/group instructions, and
Skills previews. Ordinary headings, paragraphs, lists, tables, native selection,
link shaping and highlighted code bodies are framework-owned.

Small additions in `ui/markdown` expose `TransformText`/`OnText` for the existing
application find highlighter and exact scroll reveal; `TextBlocks` uses the same
span builder for its index. `Image` supplies cancellable, account/requirement/
comment-scoped application resource loading and the shared bitmap lightbox.
`Link`/`BlockLink` provide browser-opening document cards. A paragraph containing
business media groups ordinary framework inline runs between those elements;
only text can be a child of a native RichText. `CopySource` maps projected
selection ranges back to the original bytes, including recognized annotations.
Default framework consumers retain their ordinary view/clipboard behavior.

The app's `ProjectReading` uses the existing Goldmark adjacency and whitelist
checks to omit valid Feishu image/UUID, mention and exact linkPreview metadata
from presentation only. Code, unknown comments, original/source mode, disk
caches and explicit copies retain their bytes. Framework image networking is
unused: existing trusted-resource and retry gates remain authoritative. Mermaid
is registered through `Options.Code["mermaid"]`; normal code uses the framework
highlighted Body and the original-source application copy action.

Upstream is from https://github.com/egoist/mygo/tree/v0.5.0. Module versions
are pinned in go.mod and go.sum; keep checksum validation enabled.

Targeted reading fixtures cover 420/960 DIP, light/dark, numbered-list first-line
alignment, tables and exact source/code clipboard data, metadata counterexamples,
trusted-image rejection/lightbox, independent-comment find, long body refresh and
Mermaid. This does not add automatic document virtualization or an online document
fetcher; MyGO still builds each document's blocks and caches parsing/layout.

### Reading context menu append hook

`markdown.Options.ContextMenu` appends application reading actions after native Copy, Select All and Copy as Markdown. It retains the framework selection and popup implementation; the application does not replace the Markdown menu. Precise framework/app menu, source, scoped action, empty reader, themed edge/Esc and materials tests cover the hook.
