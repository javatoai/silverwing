# MyGO local native patches

## Terminal disposal and pending process startup

`plugins/terminal/terminal.go` closes a process that finishes spawning after its
terminal was closed by draining its output, applying the existing bounded
termination fallback, and waiting for process exit before signalling Done.
`Close` also frees native view key/mouse/gesture objects under the terminal lock,
without requiring a removed tab to render another frame. `view.go` serializes
initial view publication with disposal. Headless native lifecycle regressions
cover both cases; compare/remove these patches when updating upstream MyGO.

## Upstream baseline and upgrade verification

The local module is MyGO v0.7.3, upstream commit
`6f28d7bb07b31177cf218e51586f6a5e99b38016`, with Go 1.27.3. It was rebased by
three-way comparison from v0.5.0 (`208e952608a63f0d3d9cfa8c4a069b7e3a83a943`),
the previous patched source, and the official v0.7.3 tag archive. The root
module keeps `replace github.com/egoist/mygo => ./third_party/mygo`; changing
the required version alone does not update the code that Silverwing builds.

The existing removal of upstream examples, website, documentation and repository
maintenance files is retained. Runtime resources, native sources, licenses and
the upstream and local regression tests remain present. The upgrade's original
dependency/module snapshot, source archives, SHA-256 manifests and merge decisions
are preserved in the local ignored `build/mygo-upgrade-20261029` directory.

Test the nested module separately from the application: `go test ./...` at the
repository root does not execute MyGO's tests. The v0.7.3 migration passed
`go -C third_party/mygo test -mod=readonly ./ui/editor/... ./ui/markdown/...`,
the native menu/toast/split regression group, the text package, and targeted
Windows pre-growth, resize-settling and resize-loop-scope tests. Physical IME,
mixed-DPI dragging and macOS packaging are distinct acceptance checks.

## Keyboard menu Escape and item tooltips

`ui/tooltip.go` lets an open menu own Escape for its item tooltips. A tooltip
opened by keyboard focus otherwise registered a later overlay shortcut and
consumed the first Escape, leaving the menu open. The keyboard-menu regression
covers Shift+F10, a single Escape, and restoring the owner's focus; standalone
tooltips retain their dismissal behavior. Compare/remove on an upstream upgrade.

## Themed context-menu choice delivery

`ui/menu_popup.go` defers a themed popup's chosen item until `runtime.endPass`
has consumed the current build pass's input. Delivering it inline
allowed `endPass` to clear the choice before the owning control could receive
it. The deferred delivery checks the original menu scope and closed-window
state. Task-menu UI regression tests exercise the real themed popup, including
mouse activation and bounded layout. Compare/remove on an upstream upgrade.

Themed popup activation and synchronous native-menu choices use the engine's
full-build invalidation. `showMenu` runs inside `runFrame`, where the ordinary
`requestFrame` ignores requests; requesting a host frame alone can also leave
it painting the previous tree during a drawing animation. Regressions exercise
native surface frames, repeated open/Escape and an inline host choice.

## In-frame state updates

`ui/toast.go` marks a build pass changed when a toast is added after its custom
viewport was built, and skips invalidation when closing an absent toast. If
the final permitted build pass still changes earlier content, `runtime.go`
requests a full new build after animation scheduling. The per-frame pass budget
remains bounded; settled controls do not keep requesting idle frames. Tests
cover late toast additions, changes spanning the pass budget and no-op closes.
Compare/remove these scheduling fixes on an upstream upgrade.

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
Visibility is reset on each build and established by painting; consecutive
frames can share the same clock value in the v0.7.3 tester and must not inherit
an offscreen field's previous-frame visibility.

`State.FindHighlights` paints host-owned search ranges with native match colors
without a second find bar or cursor movement. It coexists with the upstream
v0.7.3 `TextBackgrounds` API, which has independent caller-supplied colors.

`ui/text_layout.go` adds optional `TextShape.LineHeight` for the native Markdown
rich fields' 15 DIP / 1.6 line-height layout. Zero inherits `Font.LineHeight`;
an explicit nonzero value overrides it, preserving the upstream v0.7.1 API.
Layout, caret hit testing and selection all consume the same shape.

On upgrade, compare these opt-in APIs and the editor paint paths with upstream;
remove local changes when equivalent contracts are available. Functional source
limits, original bytes and document history must remain covered by the affected
tests. SDK event tests and rendering-scale simulations do not establish physical
Windows IME candidate-window or monitor-DPI acceptance.

## Native Markdown Live Preview and public buffer transactions

`ui/markdown.Editor` and `EditorState` provide one source-preserving edit surface
with one selection, native text-input client and `ui/editor.Buffer`. The active
region exposes Markdown source; inactive regions reuse native Markdown layout.
Tables, fenced blocks and other complex blocks expose their complete source
when active. `SourceMode` exposes the full source, while `ReadOnly` keeps the
same selection controller and renders the document without enabling mutation.
Display blocks do not own separate input fields or undo histories.

`ui/editor/buffer_transaction.go` exposes version-checked transactions, edit
grouping and selection metadata for controllers other than the code editor.
Rejected or stale transactions leave source and history unchanged. Undo and
redo return the associated selection. `ChangesSince` pairs copied change lists
with their current snapshot; `Snapshot.OriginalText`, `OriginalSlice` and
`WriteOriginalTo` expose original mixed line endings from that same version.
The logical parser/edit offsets remain LF-normalized UTF-8 bytes; native IME
offsets are converted from UTF-16. The preserved source tree stays part of
the same transaction and history, including background syntax snapshots.

Live Preview keeps IME preedit virtual until committed and reports composition
and edit errors to the host. The host retains permissions, source size limits,
autosave, file-baseline conflict checks and draft storage. Rendering never
serializes the parsed document back into canonical Markdown. Regressions in
`live_editor*_test.go`, `buffer_transaction_test.go` and `buffer_snapshot_test.go`
cover source fidelity, selection, history, composition and source projection.
Keep these contracts together during future upstream upgrades.

The Editor has a private container projection for lists and block quotes.
Literal source prefixes and physical empty lines define indentation and row
spacing, while inactive inline content still uses native formatted spans.
Only the selected text leaf reveals syntax; containers keep their layout.
Reader-only automatic list numbering and item margins remain in ordinary
`View` and `Stream`. Empty items and prefix glyphs participate in the same
source mapping and root input controller as body text. Heading source and
rendered runs retain their heading font, color and optional rule. A terminal
newline belongs to one visual empty row, not both the last block and a tail.
Inline image pieces report following text offsets in logical display bytes,
matching `OnText` and the source mark map even with Unicode alt text.

The Editor retains a visible source anchor across asynchronous media relayout.
The anchor is reused only while source version, selection, active range, mode,
viewport and build-time scroll offset are unchanged. Layout-driven scroll
clamping is distinguished from explicit scrolling before correcting the next
frame. Input, composition, dragging and explicit caret reveal keep their own
existing behavior; this policy does not change the shared Scroll component.

## Native Markdown code clipboard hook

Reading uses `markdown.View` and `DefaultCodeBlock`, including their native
layout, selection, highlighting and copy control. Optional `Options.CopyCode`
only delivers copying to the document host, which can preserve original CRLF
bytes and report clipboard failure. A nil callback retains upstream behavior;
false preserves the clipboard and clears the copied indicator. It does not
replace the native code-block renderer. Compare/remove on an upstream upgrade.

## Markdown code chunk source offsets

`ui/markdown/code.go` computes code-chunk UTF-8 offsets in one forward pass
when highlighted lines change, then keeps them beside the cached spans. Stable
builds no longer rescan every preceding line for every 32-line chunk. The
offsets still count the LF separating chunks, preserving `TransformText` and
`OnText` find, reveal and selection coordinates for Unicode and normalized
CRLF input. Regression tests cover chunk boundaries, streaming growth/shrink
and the hooks; the isolated 32,768-line benchmark measures offset calculation.
Compare/remove this patch when an upstream release provides equivalent offsets.

## Optional bounded toast content

`ui/toast.go` adds opt-in `Toast.ContentMaxHeight` (DIPs). Zero preserves the
upstream default rendering path. A positive value wraps the original complete
text inside a native Scroll, bounded again to the window height after reserving
toast padding and top/bottom clearance. The action remains outside the scroll.
Silverwing only opts in for its release-announcement copy-success feedback.
Toast lifetimes, hover/focus/background pauses, Escape, announcements and close
callbacks keep their upstream implementation. Compare/remove this small option
when an upstream release provides equivalent bounded toast content.

The local `mygo` module includes its original LICENSE. `go.mod` keeps the
upstream version pinned and replaces it with this source checkout. Native
runtime resources remain upstream resources.

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

`ui.SplitRatio` uses native Split pointer capture, keyboard adjustment
and accessibility behavior while storing a width fraction; double-click resets
the fraction to one half. The original size-based Split contract is retained.
The facade in `ui/api_gen.go` exposes the corresponding core component.
In v0.7.3, the ratio extension is in `ui/split_ratio.go`; upstream fixed-size
and percentage split controls keep their own current implementation. The ratio
shares the room after the one-DIP divider so equal sides wrap alike, and uses
the upstream `placeOn` layout anchor for the handle. Its facade participates in
the upstream begin/end-build boundary.

`ui/editor.DiffState.SplitRatio` applies that divider inside the actual old/new
editor layout. Each side is measured at its own width before aligning wrapped
rows. Vertical scrolling remains shared; horizontal scrolling is independent.
`DiffScrollPosition` and the editor `ScrollPosition` APIs let the application
restore lightweight reading positions without retaining stale document buffers.
Editing, source bytes, undo and clipboard transactions remain editor-owned.

On upstream upgrades compare split geometry, wrapped-line alignment, scroll
clamping and position restoration before replacing these extensions. Targeted
checks cover pointer/keyboard resizing, window resizing and independent axes.

`ui/split_ratio.go` positions the ratio handle from the current layout rather
than the previous frame's width. Ratio clamping requests a full layout rebuild,
including the subpixel-width case. `ui/editor/diffmodel.go` does not append an
extra LF when a hunk replaces an empty target; Accept and Revert retain one-step
undo. These fixes have dedicated first-frame, resize and empty-buffer tests.

`DiffState.SetPreparedHunks` accepts validated, copied hunk ranges for the current
old/new buffer identities and versions while retaining complete source buffers.
Any edit or buffer replacement invalidates that seed and resumes native diff
calculation. `diffprepared_test.go` covers rejection, context preservation and
one-step Accept/Revert against empty buffers. Workspace Git previews depend on
this extension; it must be included in upgrade comparisons.

Upstream v0.7.3 now indexes visual caret stops with a boolean slice before adding
missing stops, absorbing the local quadratic-scan fix in `internal/text/caret.go`.
`caret_bench_test.go` retains the local 4096-character benchmark alongside the
new upstream long-line benchmark; neither measures whole-window latency.

## Windows live resize presentation

The v0.7.3 upstream `internal/gpu/d3d11/d3d11.go` now owns the HWND unscaled
swap-chain policy, its stretch fallback, background fill, waitable frame pacing
and full-buffer presentation for unscaled/composition paths. The old local
swap-chain creation and unconditional source-size changes have been retired in
favor of that implementation. Cropping an oversized unscaled buffer would
magnify its scene; the upstream `shown` policy and retained settle tests check
the full source dimensions on those paths.

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

## MyGO 0.7.3 Markdown reading adapters

The local native diff divider, clipboard failure handling, source-preserving
CRLF/IME transactions, caret indexing and Windows live-resize behavior extend
the upstream interfaces. Keep those contracts when updating MyGO.

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

Upstream is from https://github.com/egoist/mygo/tree/v0.7.3. Module versions
are pinned in go.mod and go.sum; keep checksum validation enabled.

Targeted reading fixtures cover 420/960 DIP, light/dark, numbered-list first-line
alignment, tables and exact source/code clipboard data, metadata counterexamples,
trusted-image rejection/lightbox, independent-comment find, long body refresh and
Mermaid. This does not add automatic document virtualization or an online document
fetcher; MyGO still builds each document's blocks and caches parsing/layout.

### Reading context menu append hook

`markdown.Options.ContextMenu` appends application reading actions after native Copy, Select All and Copy as Markdown. It retains the framework selection and popup implementation; the application does not replace the Markdown menu. Precise framework/app menu, source, scoped action, empty reader, themed edge/Esc and materials tests cover the hook.

## Reusable Markdown reader and live structured editing

The application package `internal/ui/markdown` exposes independent native readers and editors. `Options.LinkView` wraps native formatted link children without rebuilding their source mappings. The opt-in `EditorOptions.RenderedLinks` keeps active links rendered and permits link activation while preserving drag selection; link spans receive the link theme color consistently. `InPlaceTables` keeps the table grid and maps its cells to the same root input client and Buffer. Cell edits escape structural pipes and line breaks; navigation and row/column/alignment commands share undo and source limits. `EditorState.ReplaceRange` exposes a version-checked, independently undoable source transaction for link popovers. Default upstream/editor behavior remains unchanged when the structured options are disabled. Compare these patches on the next upstream upgrade.

`ui.Tester.ScrollWith` sends explicit pointer-event modifiers for native Ctrl-wheel regression checks; the existing `Scroll` method retains its unmodified event semantics.

### Structured editor source search and request endpoints

`EditorOptions.SourceHighlights` decorates source runs without installing another input client. Public live/source search reveals selections in the same Buffer. Parser `LinkLabelRange/LinkLabelSource` shares code-aware label boundaries with link editing, preserving code/brackets when only a destination changes. Table escaping maps the requested insertion endpoint during the same transform, including retained IME suffixes. Caret-only wrapper bounds alias a hidden link prefix/suffix to the visible label edge without changing text hit/source selection mappings; regressions inspect actual accent pixels, not just input-method geometry.

- Split diff: `ui/editor/input.go` treats private `noScrollbar` as vertical-only, as its option contract specifies. Old and new panes show and hit-test their own horizontal scrollbar for overflowing unwrapped lines; horizontal positions remain independent and vertical scroll stays shared. Regression checks actual thumb pixels, dragging/wheel, no overflow, wrap and fit-content.

### In-place highlighted code and synchronized diff columns

`EditorOptions.InPlaceCode` keeps ordinary code blocks in their highlighted card while the existing root input client edits the same Buffer. `CodeBlockView.LanguageSelect` adds the native language selector to that card. Changing a language preserves the remaining fence metadata and source line endings and is one undoable edit; selecting a language for indented code converts that block to a fence. Language-specific custom renderers retain their existing contract. The application shared Markdown editor enables the option; default SDK consumers keep their prior behavior.

`parser.Document.CodeSource` exposes original fence/language/actual-closing ranges using the parser's own body segments and decoded-info mapping. Live edits use these ranges to preserve entity-separated metadata and literal fence-like body lines. Candidate fence collisions are checked with the same parser rules, including quote/list tab columns; ordinary code typing does not add a second parse.

`DiffState.SyncHorizontal` opts split comparisons into one code-column position and a common scroll range measured from both panes. Dragging, wheel input, caret/search reveal and restoration use that shared position, including when one side has shorter lines or a wider viewport. The SDK default remains independent horizontal positions. Standalone `State.HorizontalExtent` and `MinHorizontalExtent` let the application's two editable comparison inputs share the same range without replacing the native editor. Silverwing enables synchronization for its comparison viewers and editable diff tool.

### Markdown list and quote writing keys

`EditorOptions.ContinueBlocks` opts live editors into source-preserving list/quote continuation, empty-marker exit, list-item indentation and continuation-line keys. The SDK default remains literal input; Silverwing's shared editor enables the option. Structured key handling runs after native composition and in-place table handling, keeps code/HTML/frontmatter and explicit source mode literal, and uses the same Buffer, selection and undo client. Numbering follows the actual source item, without renumbering unrelated content. Whole-item indentation retains continuation text and child blocks.

### Native text-client keyboard scrolling

The engine leaves Space and Shift+Space to an active focused `TextInputClient` instead of applying browser-style page scrolling before text arrives. Native text and composition events remain unchanged. PageUp/PageDown, ordinary readers without a text client and native button/menu activation retain their keyboard behavior. Regression tests drive KeyDown, TextInput and KeyUp separately: text-only `Tester.Type` cannot detect this event-routing failure.

Markdown caret reveal now bootstraps an unpainted source-owning run on a following frame, then adjusts using that run's actual caret. It preserves source anchors through viewport/font changes and distinguishes a visible editing caret from content the user deliberately scrolled to. This avoids revealing an entire long run on each keystroke. Table cell padding has an explicit source ownership range; formatted text and links remain rendered while whitespace input, composition and history use the same document buffer.

### Table-cell arrows and adjacent code-card writing rows

In-place table arrows use the cell's real first/last visual line to cross rows in the same column, including rows made taller by another cell. Horizontal boundary arrows enter the neighboring cell, while intra-cell movement, modifier selections, IME and Tab retain their normal paths. Pointer hit testing stays within the hit cell even in its blank vertical padding; navigation does not materialize omitted cells.

Omitted trailing cells can share a source offset. A version-bound column intent and tagged display runs disambiguate their caret geometry without inserting bytes. Explicit input materializes only the required delimiters with the escaped payload in one transaction; undo restores the omitted column's editing intent as well as the original bytes.

Table runs shape with the cell's actual text alignment and context direction, including empty cells, composition and omitted positions. Shape caching includes these parameters so caret, selection and hit testing agree with the visible glyphs rather than drawing a separately shifted overlay.

Adjacent top-level closed fenced code cards reuse an existing source blank span or project one virtual writing row with the same root input client. Passive layout/focus cannot insert bytes. Explicit typing, Enter, paste or IME commit materializes the missing separator in the same source transaction, outside both fences. Source-version checks and private undo selection intent keep gap focus distinct from hidden fence navigation. Read-only cards retain spacing, source mode stays literal, custom language renderers retain their contract, and SDK options still default off.

The final closed ordinary fenced card also exposes a writing row. Its existing tail span is reused, and a missing physical newline is prefixed only on explicit input; EOF navigation never treats the document root as a following code block. Empty input/cancelled preedit stays a no-op. LanguageSelect locally removes the standard control border, keeps a restrained hover state, and retains the native keyboard focus ring and popup behavior.

Explicit EOF selections, Ctrl+End and return from source mode resolve the same outside-code tail intent before editing. Trailing real blank lines include the EOF visual row in vertical navigation.

`EditorOptions.InteractiveTasks` opts into clicking parser-confirmed task-list markers through the same root input handler. Only a stationary ordinary press/release on the same versioned marker toggles one source character in a standalone undo transaction. Drag and modified selection, composition, readonly and SourceMode retain their contracts. Public Silverwing editors enable it; SDK zero-value options and pure readers remain unchanged.

### Daily Markdown writing commands

The opt-in SmartEditing and ConfirmCodeFenceOnEnter options share the native input client and source-preserving Buffer. Explicit source command transactions, contextual selection geometry, parser literal-fence-line policy, draft history, code indentation/exit, inline formatting, block insertion/conversion, generated pairs and viewport state are reusable by host components. Default SDK callers and readers retain standard parsing. The parser policy changes display classification without rewriting Source or byte offsets; same-source policy changes invalidate parsing. Host persistence, clipboard resources and permission checks remain outside the SDK.

Structural code Enter/outdent shares literal-fence protection and draft-aware scratch parsing. Inline commands retain original source slices and nested delimiters; code-span encoding follows edge-padding rules. Parser-confirmed block prefixes and setext underlines are converted locally; consecutive renumbering keeps descendant indentation. Raw source APIs remain undecorated, while InsertMarkdown preserves virtual writing gaps and physical table rows.

Transient InsertionPoint values retain source-version, Buffer, mode, reversed selection, omitted-cell and gap intent for encoded/asynchronous insertion. AST leaf formatting avoids overlapping edits; explicit command transactions reject overlap atomically. Future styles preserve whitespace boundaries, empty styles remove their own markers, and structural block deletion retains a leading BOM. Backtick delimiter selection is linear in incoming text size.

Batch/IME formatting preserves edge whitespace and original delimiter aliases in the same transaction. Source mode retains literal key semantics and a distinct undo group. Crossed inline styles are balanced locally and checked against scoped semantic signatures before source edits; ambiguous adjoining delimiters can use an empty standard HTML comment separator. Transient planning snapshots do not attach a second editor, client or undo history. Public commands mark user interaction so late view restoration cannot supersede a newly opened command.

### Source-preserving document properties

Optional `Options.SourcePrefix` identifies a host-validated opaque prefix; `parser.SetWithPolicy` keeps it as one `SourcePrefix` leaf while retaining complete original source and absolute body offsets. Default parsing remains unchanged. The live projection renders it through `Options.Block`, protects collapsed body-entry caret/Backspace boundaries, and preserves raw Source mode. `ApplyMetadataEdits` uses the existing transaction/undo path without revealing an unrelated text caret. YAML parsing, tag validation, and property UI remain in Silverwing's reusable pure/common packages, outside the SDK and filesystem adapters.
## Writing budgets, semantic signatures and transaction history

Live TSV paste checks rectangular cell count and the source budget before
allocating the expanded grid. SourceLimit exposes the Buffer's existing budget
without granting additional writes. Inline semantic signatures use indexed span
coverage rather than a full owner scan per leaf. List/quote transactions retain
the same fence-draft, generated-pair and selection history as typed/source edits.
Regression tests cover rejection without mutation, Undo/Redo and signature
semantics; bounded benchmarks record scaling. Compare these on upgrades.
The early TSV allocation estimate uses an LF lower bound so ordinary buffers
that remember CRLF still accept exact-fit LF budgets. The final transaction
checks the actual byte representation for both ordinary and preserving buffers.

## Precise wheel compatibility and stale document comparisons

Windows wheel-derived PointerZoom retains its original precise scroll payload.
The UI engine first offers zoom to its existing consumer and only falls back to
scroll when unconsumed; native pinch events without a scroll payload do not
acquire scrolling. This preserves legacy Ctrl-wheel hosts and content transforms.
Document.Set keeps app equality callbacks outside the lock while rejecting dirty
state decisions from an older content/saved generation under the state lock.

The environment proxy resolver accepts ALL_PROXY/all_proxy as a fallback for
unset HTTP/HTTPS-specific proxies, preserving standard NO_PROXY and loopback
rules. Resolver tests use isolated subprocess environments and no real network.
Compare these local compatibility fixes on a future upstream upgrade.
