# sh2pil

`sh2pil` is a terminal session picker for **Pi**, **OpenCode**, **Claude Code**, and **Codex**
sessions. It lists the sessions of one machine or of an SSH host, groups them by project,
shows the tail of the highlighted transcript beside the list, and resumes, forks, deletes, or
opens a new one.

It is the Bubble Tea UI that a terminal key binding opens. Kitty's `Cmd+B` is the usual one.

The picker owns no store logic. It reads its rows and transcripts from the `sh2pil-sessions` helper, and
every terminal action — resume, new, fork, a shell, an editor, a zmx session — from the
`sh2pil-open` helper. Both are small Python programs that live in [`helpers/`](helpers/) and are
installed beside the binary by the Homebrew formula.

## Install

One formula installs the picker **and** its three helpers:

```sh
brew tap sashkachan/tap
brew install sh2pil
```

That puts four programs in the Homebrew `bin` directory — `sh2pil`, `sh2pil-sessions`,
`sh2pil-open`, and `sh2pil-last` — plus the reference configuration at
`"$(brew --prefix)/share/sh2pil/config.example.yaml"`. There is nothing else to install and no
build step: a release is one cross-compiled binary and three Python scripts.

### Where the helpers go, and why

The four programs must sit in **one directory**, because `sh2pil` finds the helpers through its
own executable, and the helpers find each other the same way:

```go
helperDir = filepath.Dir(os.Executable())   // *.go
path = pathlib.Path(__file__).resolve().parent / 'sh2pil-open'   // helpers
```

That is what makes the install work from a key binding, where `PATH` is minimal and holds
neither the Homebrew directory nor `~/.local/bin`:

```
$(brew --prefix)/bin/sh2pil           -> ../Cellar/sh2pil/<version>/bin/sh2pil
$(brew --prefix)/bin/sh2pil-sessions  -> ../Cellar/sh2pil/<version>/bin/sh2pil-sessions
$(brew --prefix)/bin/sh2pil-open      -> ../Cellar/sh2pil/<version>/bin/sh2pil-open
$(brew --prefix)/bin/sh2pil-last      -> ../Cellar/sh2pil/<version>/bin/sh2pil-last
```

Both the `bin` directory and the Cellar directory hold all four, so the lookup succeeds whether
the platform reports the symlink path or resolves it.

A launcher that starts a copy from somewhere else will not find the helpers. Point at the real
ones with `SH2PIL_SESSIONS`, `SH2PIL_OPEN`, and `SH2PIL_LAST` instead of moving the binary.

### Without Homebrew

Take the archive for your platform from
[Releases](https://github.com/sashkachan/sh2pil/releases) and keep the four programs together:

```sh
tar xzf sh2pil_<version>_darwin_arm64.tar.gz
cd sh2pil_<version>_darwin_arm64
mkdir -p ~/.local/bin
install -m 0755 sh2pil ~/.local/bin/sh2pil
install -m 0755 helpers/sh2pil-sessions helpers/sh2pil-open helpers/sh2pil-last ~/.local/bin/
```

`install -m 0755` keeps the executable bit the archive carries. `~/.local/bin` is on `PATH` for
an interactive shell but not for a key-binding child, so use absolute paths in a key binding.

### Check the install

```sh
sh2pil --version
sh2pil --print-config            # the effective settings
sh2pil-sessions harnesses --json # which session stores this machine has
sh2pil-open live --json          # the chats a pi process is running here
```

## Requirements

`sh2pil` needs **python3** to run and read sessions, and **zsh** to open anything. Every other
entry below is a store, a terminal, or a tool an action opens, and **each of those is optional**:
a missing one is reported in the header or on the row rather than failing the read.

| Dependency | Needed for | Notes |
|---|---|---|
| **python3** 3.9+ | the helpers themselves | Standard library only. Resolved automatically, in the order `SH2PIL_PYTHON`, the build-time path, `$(brew --prefix)/bin`, `/usr/local/bin`, `/usr/bin`, `PATH` — so a key-binding child's `PATH` need not hold it. |
| **zsh** | every action that opens something | Launches and tool runs go through `zsh -lic`, so a `?rc`/`?profile` can put the Homebrew directory on the child's `PATH`. Without zsh the reads still work; the actions cannot start. |
| **Pi** | `pi` rows and chats | Store `~/.pi/agent/sessions`; the `pi` CLI resumes and forks. |
| **Claude Code** | `claude` rows and chats | Store `~/.claude/projects`. |
| **Codex** | `codex` rows and chats | Store `~/.codex/sessions`. |
| **OpenCode** | `opencode` rows and chats | Keeps no files to read: the `opencode` CLI *is* the store, so it is discovered on `PATH`. |
| **zoxide** | the project list | Its database is what `zi` offers, so without it there are no project groups — only sessions. Read on remote targets too. |
| **zmx** | chats that outlive their window | The zmx pane, the `⚡` marker, and the state column. Found on `PATH`, then `~/.local/share/mise/shims/zmx`, `$(brew --prefix)/bin/zmx`, `~/.local/bin/zmx`. |
| **git** | the lazygit action | `/usr/bin/git`, then `PATH`; used to find the repository root. |
| **ssh** | remote targets | A master on `~/.ssh/agent.sock` is reused, and is shared with plain `ssh` when `ssh_config` points `ControlPath` at it. |
| **kitty** | the overlay bindings | `kitten`, which ships with kitty, does the window lookup and remote control. |
| **tmux** | the fallback terminal | Used when kitty cannot be reached, and for panes inside it. |
| **nvim** | the transcript and editor actions | `$EDITOR` wins over it. |
| **lazygit**, **yazi** | two travel actions | Absent tools are reported and skipped. |
| **mise** | *not required* | Only one more place to find `zmx`: `~/.local/share/mise/shims/zmx` is tried after `PATH`, locally and on a remote host. `zmx_remote_binary` can name it explicitly. |

macOS needs nothing extra for the picker itself: `defaults` reads the system appearance so the
preview matches the terminal.

### Remote targets

A host needs `python3`, and `zsh` or `bash` for its login shell. It does **not** need `sh2pil`
installed: when a host has no helper, the picker sends the code over the connection and runs it
with `python3 -`. A host that still carries the older helper names — `pib`, `pib-open`,
`pi-last` — is used through a fallback chain, so an unmigrated machine keeps working.

### The live state column

The `● needs you` / `● bash` / `● idle` column is published by a small Pi extension that writes a
record beside each chat, and it is not installed by this formula — see
[`helpers/README.md`](helpers/README.md#the-live-state-column). Without it the rows still read;
they just say `live`, which is what every row said before the state existed.

## Configure

Copy [`config.example.yaml`](config.example.yaml) to `~/.config/sh2pil/config.yaml` and edit
it. Every setting has a default, so an empty or absent file is valid. A file under
`~/.config/sh2pil/config.d/` overrides the main file in name order, which lets one machine
change a setting without touching the tracked file.

```sh
mkdir -p ~/.config/sh2pil
cp "$(brew --prefix)/share/sh2pil/config.example.yaml" ~/.config/sh2pil/config.yaml
sh2pil --check-config     # print the effective settings and keybindings
sh2pil --print-config     # print the effective settings only
```

`--config <file>`, or `SH2PIL_CONFIG`, reads a different main file. `SH2PIL_PYTHON` selects the
interpreter the helpers run under.

## Terminal integration

See [`helpers/README.md`](helpers/README.md) for the key bindings of each terminal.

- **kitty** — `Cmd+B` opens the picker as an overlay; `f3`/`f4` read the session of the
  window they are pressed in; `Cmd+.` opens the picker's action menu for that window.
- **tmux** — bind a key to `sh2pil` and run it in a popup.
- **plain shell** — run `sh2pil` directly.

## How it reads a machine

Rows come from the stores the machine runs: Pi, OpenCode, Claude Code, and Codex. A store
that a machine does not run is no error — it contributes no rows and the header says so.

A **target** is this machine or one configured SSH host, and `{` and `}` move between them.
Each target reads its own zoxide projects, its own sessions, and its own live zmx sessions,
so a host is read as a whole and a session is never shown beside a row that lives elsewhere.
Nothing is read at startup but this machine: a remote target connects when it is first
visited, which is when a YubiKey touch can be answered.

The ignored projects are a list in the picker's own state (`alt+i` shows them), and they
apply to this machine only.

## Build from source

```sh
git clone https://github.com/sashkachan/sh2pil
cd sh2pil
go build -trimpath -ldflags "-s -w -X main.version=dev" -o sh2pil .
go test ./...
python3 -m unittest discover -s helpers/tests -t helpers/tests -p 'test_*.py'
```

`doc.go` is the picker's full specification.

## Releases

A `v*` tag starts the release workflow on the self-hosted runner. It builds `linux/amd64` and
`darwin/arm64` archives, attaches them and their checksums to a GitHub release, and pushes the
updated formula to the [`sashkachan/homebrew-tap`](https://github.com/sashkachan/homebrew-tap)
repository when the `TAP_TOKEN` secret is set.

## Layout

| Path | What it is |
|---|---|
| `*.go` | the picker, a single `main` package |
| `helpers/sh2pil-sessions` | session stores, the command line, and the shared actions |
| `helpers/sh2pil-open` | terminals, zmx, and the remote verbs |
| `helpers/sh2pil-last` | render one transcript |
| `helpers/tests/` | unit tests for the helpers, run against the sources |
| `config.example.yaml` | a commented reference configuration |

## License

MIT. See [`LICENSE`](LICENSE).
