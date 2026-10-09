# Helpers

`sh2pil` keeps no store logic of its own. Three small Python programs do that work, and they
are installed beside the binary:

| Helper | What it does |
|---|---|
| `pib` | reads the session stores (Pi, OpenCode, Claude Code, Codex), lists or shows a session, and deletes or renames one |
| `pib-open` | opens a terminal or an editor for a session, and owns the zmx verbs |
| `pi-last` | renders one transcript |

The Homebrew formula installs all four programs into the same `bin` directory, because
`sh2pil` runs the helpers it finds beside its own binary. Each helper also works on its own
from a shell:

```sh
pib list --harness pi --json
pib show <id> --harness claude
pib projects --json
pib-open live --json
pi-last --session ~/.pi/agent/sessions/<project>/<file>.jsonl --full
```

Every helper uses the Python standard library only.

## Find the install path

```sh
brew --prefix            # for example /opt/homebrew
ls "$(brew --prefix)/bin" | grep -E '^(sh2pil|pib|pib-open|pi-last)$'
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
map f3 launch --allow-remote-control --type=overlay $PREFIX/bin/pib last --window @active-kitty-window-id --nvim $NVIM

# f4 = the whole transcript of that same session.
map f4 launch --allow-remote-control --type=overlay $PREFIX/bin/pib last --window @active-kitty-window-id --full --nvim $NVIM

# cmd+. = the picker's action menu for this window.
map kitty_mod+. launch --allow-remote-control --type=overlay $PREFIX/bin/pib-open window-menu --window @active-kitty-window-id
```

`--allow-remote-control` is required, because a key-binding child has no `KITTY_*`
environment of its own and the picker opens tabs and windows. `--type=overlay` draws the
picker over the current window.

## tmux

Bind a key to the picker in a popup:

```conf
bind b display-popup -E "sh2pil"
```

`pib-open` uses kitty when it can, then tmux, then a printed command. Inside tmux it opens a
new pane or window for a session, so a binding for `pib` and `pib-open` is optional.

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
| `PIB_CONFIG` | the main settings file, instead of `~/.config/sh2pil/config.yaml` |
| `PIB_MD_STYLE` | the glamour style for the preview: `dark`, `light`, `notty`, or a style file |
| `SH2PIL_PYTHON` | the interpreter the helpers run under |
| `SH2PIL_PATH` | the picker binary that `pib-open` runs for the window menu |
| `PIB_OPEN` | the `pib-open` binary that `pib` runs |
| `PIB_PI_LAST` | the `pi-last` binary that `pib` runs |

## Tests

The tests run against the sources, not an installed copy:

```sh
python3 -m unittest discover -s helpers/tests -t helpers/tests -p 'test_*.py'
```
