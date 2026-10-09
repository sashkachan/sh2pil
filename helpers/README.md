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

## Find the install path

```sh
brew --prefix            # for example /opt/homebrew
ls "$(brew --prefix)/bin" | grep -E '^(sh2pil|sh2pil-sessions|sh2pil-open|sh2pil-last)$'
```

A terminal key binding usually starts the program with a **minimal PATH** that does not
include the Homebrew `bin` directory. Use absolute paths in a key binding. The examples below
use `$PREFIX` for the output of `brew --prefix`, and `$NVIM` for the path of your editor.

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
