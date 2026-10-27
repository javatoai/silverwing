# silverwing-qa

Native Go / MyGO desktop workspace for QA colleagues. Branch: `release/snb-qa`.

Keep tasks, requirements and comments, material browsing and text editing, material Git synchronization, Codex, Skills, plugins, MCP, AGENTS templates, JSON/date/text tools and local port management.

Code repositories, service modules, Worktree, code Git actions, Tag builds, Genbu, production releases, IDE and Cursor integration have been removed.

The three task tabs are Materials, Requirement details and Task notes. Materials opens by default. Create a task from the requirements page without configuring any code repository. Configure Meegle, material paths and optional Codex in Settings.

Application data defaults to `~/silverwing-qa`; tasks and materials live under that directory. QA has its own installer identities and configuration discriminator (`edition: qa`). Developer configuration and tasks are not migrated automatically.

## Build

Use the Go version specified in `go.mod`.

```powershell
go test -timeout 10m ./...
pwsh -File scripts/build-windows.ps1 -PortableOnly
```

Unzip the complete Windows package and run `silverwing-qa/silverwing-qa.exe`, keeping `resources` beside it. Windows x64 EXE/MSI/ZIP and macOS arm64 DMG are supported packaging targets. See [release instructions](docs/RELEASE.md) and [edition scope](docs/QA-EDITION.md).

QA release tags use `qa-vX.Y.Z`, assets use `silverwing-qa-*`, and QA releases must never become the repository latest release. `release/snb-product` continues from the QA edition with identical features and separate installation/data identities.

MIT licensed. Bundled dependencies retain their respective licenses; package builds generate third-party notices.
