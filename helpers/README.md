# Helpers

`sh2pil` keeps no store logic of its own. Three small Python programs do that work, and they
are installed beside the binary:

| Helper | What it does |
|---|---|
| `sh2pil-sessions` | reads the session stores (Pi, OpenCode, Claude Code, Codex), lists or shows a session, and deletes or renames one |
| `sh2pil-open` | opens a terminal or an editor for a session, and owns the zmx verbs |
| `sh2pil-last` | renders one transcript |

The Homebrew formula installs all four programs into the same `bin` directory, because
`sh2pil` runs the helpers it finds beside its own binary. Each helper also works on its own
from a shell:

```sh
sh2pil-sessions list --harness pi --json
sh2pil-sessions show <id> --harness claude
sh2pil-sessions projects --json
sh2pil-open live --json
sh2pil-last --session ~/.pi/agent/sessions/<project>/<file>.jsonl --full
```

Every helper uses the Python standard library only.

## Install

There is nothing to install separately. The helpers and the picker are **one formula** and
**one archive**, and they must end up in **one directory**.

### Homebrew

```sh
brew tap sashkachan/tap
brew install sh2pil
```

Four programs appear in `$(brew --prefix)/bin`: `sh2pil`, `sh2pil-sessions`, `sh2pil-open`,
`sh2pil-last`. Homebrew keeps the real files in `$(brew --prefix)/Cellar/sh2pil/<version>/bin`
and symlinks each one into `bin`; both directories hold all four, so the sibling lookup works
either way, and the executable bit comes from the archive.

### Without Homebrew

The release archive keeps the helper scripts under `helpers/`. Flatten them next to the picker:

```sh
tar xzf sh2pil_<version>_darwin_arm64.tar.gz
cd sh2pil_<version>_darwin_arm64
mkdir -p ~/.local/bin
install -m 0755 sh2pil ~/.local/bin/sh2pil
install -m 0755 helpers/sh2pil-sessions helpers/sh2pil-open helpers/sh2pil-last ~/.local/bin/
```

### On a host with no helper at all

Nothing is needed. `sh2pil-open` sends a helper's own code over the SSH connection and runs it
with `python3 -` when the host has no copy, or an older one. The host needs `python3`, and
`zsh` or `bash` for its login shell.

### Why one directory

```go
helperDir = filepath.Dir(os.Executable())                        // the picker
path = pathlib.Path(__file__).resolve().parent / 'sh2pil-open'   // the helpers
```

A key-binding child gets a minimal `PATH`, so nothing is looked up in it. To keep a copy
somewhere else, name it with `SH2PIL_SESSIONS`, `SH2PIL_OPEN`, or `SH2PIL_LAST` rather than
moving one program away from the others.

### Check the install

```sh
sh2pil --version
sh2pil-sessions harnesses --json   # [{"harness":"pi","present":true,"reason":""}, ...]
sh2pil-open live --json            # the chats a pi process is running here
sh2pil-last --help
```

## Dependencies

Only **python3** is needed to run the picker and read sessions; a **login shell** is needed for
any action that opens something. The rest are stores, terminals, and the tools an action opens,
and each of those is optional — a missing one is reported, not fatal.

| Dependency | Needed for | Notes |
|---|---|---|
| **python3** 3.9+ | the helpers | Standard library only. Resolved in the order `SH2PIL_PYTHON`, the build-time path, `$(brew --prefix)/bin`, `/usr/local/bin`, `/usr/bin`, `PATH`, so a minimal `PATH` is fine. |
| **a login shell** | every action that opens something | New windows and tool runs use `<shell> -lic`, so the child reads your login and rc files and finds the Homebrew directory. Resolved as `$SHELL`, then the passwd entry, then `bash`, then `sh`; an installed shell is required, a particular one is not. |
| **zoxide** | the project list | `sh2pil-sessions projects`, and the remote equivalent, read `zoxide query -l`. Without it there are no project groups. |
| **zmx** | chats that outlive their window | The zmx pane, the `⚡` marker, the state column. Discovered on `PATH`, then `~/.local/share/mise/shims/zmx`, `$(brew --prefix)/bin/zmx`, `~/.local/bin/zmx`. |
| **mise** | *nothing* | Only another place to find `zmx`, through its shim. Optional; `zmx_remote_binary` can name the shim explicitly. |
| **git** | the lazygit action | Used to find the repository root. |
| **ssh** | remote targets | For the master socket the reads and the actions share. |
| **kitty** / **tmux** | the terminals | `kitten` does the window lookup and remote control; tmux is the fallback, and the pane host. |
| **nvim**, **lazygit**, **yazi** | the actions that open them | Defaults: the `tools.editor`, `tools.file_browser`, and `tools.git_tool` settings replace them, and a `tools.hosts.<destination>` entry replaces one for one host. |
| **Pi**, **Claude Code**, **Codex**, **OpenCode** | the rows of that store | The three file-backed stores are read from `~/.pi/agent/sessions`, `~/.claude/projects`, and `~/.codex/sessions`; OpenCode is read through its CLI. |

## The live state column

The `● needs you`, `● bash`, and `● idle` marks come from records written by a Pi extension
that is **not** shipped here — it belongs to the dotfiles setup this tool grew out of. The
extension writes one file per running chat into `~/.local/state/pib-open/live`, which the
helper reads. Without it nothing breaks: a row simply says `live`, which is what every row said
before the state existed.

Write records with a `state`, a tool *name*, a dialog kind, and the dialog's short label — a
status, never content. See `sh2pil-open state --json` for the shape it reads.

## Paths in the examples

```sh
brew --prefix            # for example /opt/homebrew
```

A terminal key binding usually starts the program with a **minimal PATH** that does not
include the Homebrew `bin` directory. Use absolute paths in a key binding. The examples below
use `$PREFIX` for the output of `brew --prefix` and `$NVIM` for the path of your editor.

## kitty

Add to `~/.config/kitty/kitty.conf`:

```conf
# cmd+b = the session picker, as an overlay.
map kitty_mod+b launch --allow-remote-control --type=overlay $PREFIX/bin/sh2pil --nvim $NVIM

# f3 = the last finished message of the session in this window, in the editor.
map f3 launch --allow-remote-control --type=overlay $PREFIX/bin/sh2pil-sessions last --window @active-kitty-window-id --nvim $NVIM

# f4 = the whole transcript of that same session.
map f4 launch --allow-remote-control --type=overlay $PREFIX/bin/sh2pil-sessions last --window @active-kitty-window-id --full --nvim $NVIM

# cmd+. = the picker's action menu for this window.
map kitty_mod+. launch --allow-remote-control --type=overlay $PREFIX/bin/sh2pil-open window-menu --window @active-kitty-window-id
```

`--allow-remote-control` is required, because a key-binding child has no `KITTY_*`
environment of its own and the picker opens tabs and windows. `--type=overlay` draws the
picker over the current window.

## tmux

Bind a key to the picker in a popup:

```conf
bind b display-popup -E "sh2pil"
```

`sh2pil-open` uses kitty when it can, then tmux, then a printed command. Inside tmux it opens a
new pane or window for a session, so a binding for `sh2pil-sessions` and `sh2pil-open` is optional.

## Plain shell

Run the picker directly:

```sh
sh2pil
```

Useful flags:

```sh
sh2pil --check-config     # effective settings and keybindings
sh2pil --print-config     # effective settings only
sh2pil --version
sh2pil --config ~/alt.yaml
```

## Environment

| Variable | Effect |
|---|---|
| `SH2PIL_CONFIG` | the main settings file, instead of `~/.config/sh2pil/config.yaml` |
| `SH2PIL_MD_STYLE` | the glamour style for the preview: `dark`, `light`, `notty`, or a style file |
| `SH2PIL_PYTHON` | the interpreter the helpers run under |
| `SH2PIL_PATH` | the picker binary that `sh2pil-open` runs for the window menu |
| `SH2PIL_SESSIONS` | the `sh2pil-sessions` binary that `sh2pil-open` runs |
| `SH2PIL_OPEN` | the `sh2pil-open` binary that `sh2pil-sessions` runs |
| `SH2PIL_LAST` | the `sh2pil-last` binary that `sh2pil-sessions` runs |

Each variable also accepts the name an earlier release used (`PIB_CONFIG`, `PIB_MD_STYLE`,
`PIB_TUI_PATH`, `PIB_PATH`, `PIB_OPEN`, `PIB_PI_LAST`), so a machine that still has the older
dotfiles keeps working while it is migrated.

## Compatibility with the dotfiles

`sh2pil` reads and writes three things that the dotfiles also use. Their names are older than
this repository and are kept unchanged:

| Name | Shared with |
|---|---|
| `PIB_ZMX` | the `pib-live` Pi extension, which reads it to find the zmx binary |
| `~/.local/state/pib-open/live` | the same extension, which writes the live session records there |
| `~/.ssh/pib-sk-%C` | `~/.ssh/config.d/remote-zmx.conf`, which shares the master socket |

A remote host may still carry the older helper names. `sh2pil-open` tries the current name
first and falls back to `pib`, `pib-open`, and `pi-last`, so an unmigrated host answers instead
of being sent the whole helper on every read.

## Tests

The tests run against the sources, not an installed copy:

```sh
python3 -m unittest discover -s helpers/tests -t helpers/tests -p 'test_*.py'
```
