# sh2pil

`sh2pil` is a terminal session picker for **Pi**, **OpenCode**, **Claude Code**, and **Codex**
sessions. It lists the sessions of one machine or of an SSH host, groups them by project,
shows the tail of the highlighted transcript beside the list, and resumes, forks, deletes, or
opens a new one.

It is the Bubble Tea UI that a terminal key binding opens. Kitty's `Cmd+B` is the usual one.

The picker owns no store logic. It reads its rows and transcripts from the `pib` helper, and
every terminal action — resume, new, fork, a shell, an editor, a zmx session — from the
`pib-open` helper. Both are small Python programs that live in [`helpers/`](helpers/) and are
installed beside the binary by the Homebrew formula.

## Install

```sh
brew tap sashkachan/tap
brew install sh2pil
```

The formula installs `sh2pil` and its three helpers — `pib`, `pib-open`, and `pi-last` — into
the same `bin` directory, because the picker runs the helpers it finds beside its own binary.

Requirements:

- **python3** (3.9 or newer). The helpers use the standard library only.
- **ssh** for remote targets.
- **zmx** (optional) to run chats that outlive their window.
- **kitty** or **tmux** (optional) for the terminal integration below. Without them the
  helpers print the command instead.

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

`--config <file>`, or `PIB_CONFIG`, reads a different main file. `SH2PIL_PYTHON` selects the
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
| `helpers/pib` | session stores, the command line, and the shared actions |
| `helpers/pib-open` | terminals, zmx, and the remote verbs |
| `helpers/pi-last` | render one transcript |
| `helpers/tests/` | unit tests for the helpers, run against the sources |
| `config.example.yaml` | a commented reference configuration |

## License

MIT. See [`LICENSE`](LICENSE).
