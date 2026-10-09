// Command sh2pil is the terminal UI for Pi, OpenCode, Claude Code, and Codex sessions, with the
// tail of the highlighted transcript beside the list. Kitty's Cmd+B binding opens it.
//
// It owns no policy. Sessions and transcript text come from `sh2pil-sessions`; project directories come
// from zoxide's database (the same locations offered by `zi`); the live zmx sessions and every
// terminal action come from `sh2pil-open`.
//
// One view serves every machine: a grouped list of projects, with each project's sessions
// under it, for one target at a time. A target is this machine or one configured SSH host, and
// { and } move to the previous and the next one. The project list opens a group with enter or
// tab and closes it again;
// the header carries the project, its directory, and how many sessions are under it. Each target
// reads its own zoxide projects, its own sessions, and its own live zmx sessions, so a host is
// read as a whole and a session is never shown beside a row that lives elsewhere. The zmx
// sessions of the target appear in a pane under the project list, which [ and ] move the
// cursor into; the preview follows whichever pane has it. A row that runs inside a zmx session
// is marked with `⚡` in the project list, and its zmx row is in the pane below.
//
// Nothing is read at startup but this machine. A remote target connects when it is first
// visited, which is when the reader can answer a YubiKey touch, and the target's rows are cached
// so a switch back is instant. A target switch schedules a delayed read, so rotating through the
// tabs starts one read rather than one per tab, and the 30 second poll refreshes only the target
// on screen.
//
// The loaded rows of a project are the files in its directory, a session shows the tail of its
// own transcript, and a zmx session shows the transcript of the chat it carries, or what the
// session is when it carries no chat yet. A project lists the sessions of every store the picker
// is set to read, side by side, in one list per project: a store is not a filter, and a row says
// in its own column which store wrote it, so a chat is read where it is rather than recalled
// through a switch. The header names the stores this target is read for: a store in force for a
// new chat is bold, a store the target does not have is red, and a store the target did not
// answer about is dim. A store that a machine does not run is no error: it contributes no rows,
// and the header says so, which is how a host that runs three of the four stores looks like
// itself. A host that runs none of them is a target like any other: its session read answers
// with an empty list, so the rest of its read, its projects and its own zmx sessions, arrives as
// usual. The ignored projects are a list in this picker's own state: alt+i shows the ignored
// groups instead of the active ones, and only for this machine, because a host cannot be told to
// ignore a directory here. The store in force selects the backend for a new chat; opening a
// listed session always uses its own. ctrl+b cycles the stores the target on screen has, so it
// never selects one that would answer with nothing. A Claude Code row is read from its own
// transcript at ~/.claude/projects and resumes by session id, forks with --fork-session, and is
// deleted with the file; a Codex row is read from its rollouts at ~/.codex/sessions and resumes
// with `codex resume`, forks with `codex fork`, and is deleted with its rollout. Only Pi keeps a
// record of the process that runs a chat, so only a Pi row is marked live; a Claude Code or Codex
// resume never asks about a second writer. A live Pi row says more than that: the extension that
// writes the record also publishes what the chat is doing, so the row carries the state beside
// the flag -- `● needs you` for a chat blocked on a person, `● bash` for the tool it is running,
// `● idle` for one that has settled, and `● live` when nothing said what it is doing, which is
// every record written before the state existed. The preview names the dialog a blocked chat is
// waiting on, and how long it has waited. A group header carries `• waiting` too, because a group
// starts closed and the row worth finding would otherwise be hidden inside it. A state whose clock
// has stopped is shown as `● unknown` and never as a running one: a wedged process must not read
// as busy. Only the state is read on a clock (`state_poll_seconds`, three seconds by default,
// zero for none at all); the process and the window that shows it are read when an action asks,
// and every tenth tick, because those change rarely. A host's chats carry their state too: its own
// extension writes its records, its own helper reads them, and this picker asks it for them over
// the connection that host already has. A host is read on a slower clock and only while its master
// is up, so a clock never asks for a connection and never needs a YubiKey touch; a host that runs
// older dotfiles answers with no state at all, and its rows say `live`. The words are the same
// words on both machines, and a notification about a host's chat names the host, because the same
// project exists on more than one machine.
//
// alt+l keeps only the rows a window already shows, which are the ones the reader can go back
// to: a live session on this machine, and a zmx session a terminal is attached to. A row on
// another host has no owner to judge by, so it is not one of them.
//
// The ways into a row are two menus, because they are two different kinds of thing.  The dot
// key opens travel: the ways into the project space the row belongs to, which is the chat
// itself, a shell, the git tool, the file browser, and the editor.  Cmd+. draws that same menu
// for the window it was opened from, because a window's menu is about getting somewhere in its
// own directory.  Everything else is a command, and the command palette (alt+p) carries them
// under a heading per group: the delete interface first, then the session commands, then the
// view, the target, and the picker itself.  A palette row is a bound action, so it is run the
// way its own key would run it: the rules about a running session, a row on another host, or a
// store that cannot be renamed stay in the one place that already knows them.  An action the
// reader unbound is not offered at all, so the palette never shows a row that would do nothing.
//
// The first command is the delete interface (alt+d): it removes the sessions older than an age.
// The age is one field -- `1d`, `3h`, `10m`, or a combination such as `1d3h10m` -- and a number
// with no unit is refused rather than guessed at, because an age guessed wrong deletes the wrong
// week.  Nothing goes before the count is shown: the picker reads the selection first and asks
// about the number, since a transcript has no trash.  An age covers the target on screen and no
// other: this machine's stores are read here, and a host's stores are read and cleaned there,
// over the connection that host already has.  Both halves of a target are counted -- the
// transcripts of every store the picker reads, and the zmx sessions of the same target -- and
// each one that is in use is left alone: a chat a pi process is still writing, the zmx session
// the picker itself runs inside, and a zmx session a terminal is attached to.  `sh2pil-sessions prune
// --older-than 1d3h --zmx --yes` is the same thing from the command line, and without `--yes`
// it only reports what it would delete.
//
// The state is a queue and not only a column. The chats that wait on a person sort to the top of
// their own group, `n` moves the cursor to the next one and opens its group, and a chat that
// starts waiting while the picker runs carries `•` in the marker column until the cursor reaches
// it. Only a chat blocked on a prompt is lifted: a chat that has settled is what every finished
// chat is, so lifting that too would lift most of the list. Only the project list is ranked: the
// groups keep their own order, and the zmx pane keeps the order it was read in. `attention_sort`
// (true by default) turns the ranking off.
//
// A chat that starts waiting is announced in one message, which names a project and a state and
// nothing else: never prompt text, a tool argument, a path, or a transcript excerpt. `notify`
// chooses how it arrives: `terminal` raises a desktop notification (OSC 9, the default), `bell`
// rings the terminal bell, and `off` leaves the unread mark alone to carry the news. The message is
// for a reader who is not looking at the picker, so a picker that knows it is in front stays quiet:
// its mark and its state column are already there to be read. It learns that from terminal focus
// reports, and a terminal that never answers leaves the picker assuming it is not in front, which
// speaks up rather than falling silent. Cmd+B leaves a new picker behind every time and an
// overtaken one keeps running, so the pickers agree on a single speaker: the newest of them, which
// is the one the next key reaches. A chat that was already waiting when the picker opened is not
// news, and a burst of chats that start waiting together makes one message.
//
// Cmd+. asks the travel question without opening the picker first: it draws that same menu for
// the window the reader is in, as a dialog over that window. The project the menu applies to is
// the one that window is in, and the host is the one it runs on: the window's own processes name
// its host, and the zmx session it holds a client for, the chat it runs, or, for a window on this
// machine, the directory kitty reports name its project. So one key works at a local prompt and in
// a window on a host the picker reads. A window none of those rules places is refused in one
// line, as is an ssh to a destination outside zmx_servers, because a menu on the wrong project is
// worse than no menu. Every action from the dialog asks where its terminal goes, whatever the
// placement preference remembers, and a new chat asks which store first, over the stores the
// picker reads that the window's host has. The dialog names the project and host it applies to,
// and closes once the action is under way.
//
// Transcript text is Markdown, so the preview pane renders it with glamour. The shared config
// file ~/.config/sh2pil/config.yaml controls which stores are read (`harnesses: pi, claude`, or
// every store this build knows when the key is absent), which target opens first
// (default_target: local, or one of the zmx_servers destinations), whether the picker closes
// after a launched action (close_on_navigate, default true), whether a new, resumed, or
// forked chat runs inside a zmx session of its own (zmx, default false; sh2pil-open opens and
// labels that session), how the rows that wait on a person are ranked (attention_sort), and how
// such a row is announced (notify).
//
// Every keybinding is a setting too: `key.<context>.<action>` moves one action, a comma list
// gives it several chords, and `none` disables it, for example `key.list.fork: alt+p`.
// `config.d/*.yaml` overlays the main file, in name order, so a machine or a session changes a
// setting without editing it. `--config` and `SH2PIL_CONFIG` select another file. `--check-config`
// prints the effective configuration and the bindings in force, and `--print-config` prints the
// settings alone. The directory tools are settings too: editor, file_browser, and git_tool
// name the programs the transcript, the alt+f action, and the ctrl+g action run, and a remote
// row runs the tool the host's own config names.

// mode: keybind runs the primary key directly, prompt opens the numbered next-step menu, and
// both keeps the keys and offers the menu; the dot key opens the menu in any mode, and
// trigger.<action>: prompt moves one action into it and releases its key.
//
// Add SSH config aliases or direct `user@host` destinations as
// `zmx_servers: host-a, user@computer` to add them to the target bar. A host is asked for its
// own sessions and its own zoxide projects, which are grouped exactly as the local machine's
// are, so every key works the same way on either. The host needs nothing installed for a session
// read: the picker's own `sh2pil-sessions` and `sh2pil-last` travel to it when it has no copy, so both ends read
// the store with one version of the code, and no file is written there. A host that does run the
// dotfiles answers with its own copy and also marks the sessions its pi processes run. A remote
// row that runs inside a zmx session on that host is reached by attaching to that session: enter
// and ctrl+t open a client, which is what the local `switch` does too, because the chat keeps the
// process that already writes it and no second writer appears. Any other remote row is a process
// that only that host can start, so enter resumes it over SSH, ctrl+t opens another terminal for
// it, ctrl+f forks it, and ctrl+a starts a new chat in its project group; a row the host reports
// as already running is asked about first. The copied command (ctrl+y) is the SSH one. The keys
// that act on this machine's store or directories stay local: rename and the transcript editor
// say where the session is instead.  The delete is the exception: the store that holds a
// transcript is the one that removes it, so ctrl+w acts on the host too, and so does the
// age-based delete, which resolves its age against the rows that host holds rather than the
// rows this machine can see.  The directory keys are
// the other exception: on a session or project row of another host, ctrl+g, alt+f, and ctrl+x
// open on that host in the directory it recorded, because lazygit and yazi act on the files they
// show and a shell is the host's own, and alt+e hands the project to that host's editor, which is
// its `$EDITOR` or nvim.  A directory the host has reported as gone is refused by name, instead
// of opening that host's home directory by surprise.  A shell or an editor gets no zmx session,
// here or there: only a chat is worth keeping alive across terminals, and a remote zmx row still
// only attaches to that session and kills it.
//
// For remote access, sh2pil uses Homebrew OpenSSH and the agent at ~/.ssh/agent.sock. It uses
// public-key authentication only; it does not fall back to password or keyboard-interactive
// login. The server must have the user's public key in ~/.ssh/authorized_keys. Keep the YubiKey
// with that key available, and run `yubi-load` after swapping tokens. When a host is first
// visited and it has no master connection, sh2pil suspends the UI while OpenSSH connects, so you
// can touch the key. The config lists two YubiKey identities, so ssh may offer the token that is
// not inserted first: that attempt is rejected and ssh moves to the token that is present. When
// the rejected attempts exhaust MaxAuthTries, the connect is retried up to three times before
// the failure is reported. sh2pil reuses the master for later reads, polls every 30 seconds, and
// keeps the master for up to 12 hours. The host-specific SSH config at
// ~/.ssh/config.d/remote-zmx.conf shares the master with regular SSH commands to this host, so
// new Kitty panes reuse it. Other hosts are not affected. Press ctrl+r to refresh the target on
// screen and to reconnect when its master is gone. A host that cannot be read keeps the rows it
// already had, and the status line carries the reason.
//
// Remote zmx discovery checks PATH, then the standard mise shim; `zmx_remote_binary` can
// override its path. Remote zoxide queries run in the host's own login shell, which is zsh
// when the host has one and bash when it does not, so the host's `zi` setup provides its
// PATH; `zoxide_remote_binary` can override the executable path when needed. A remote
// session read needs no install on that host: the helper is looked up in PATH and then in
// ~/.local/bin, and when the host has neither, the picker's own copy travels to it over the
// connection, so the same code reads the store on both ends. A host that runs the dotfiles
// also answers with its own copy, and only then can it say which sessions its pi runs.
//
// `ssh_env` names the environment every session on every host is given, as comma-separated
// NAME=value pairs, and `ssh_env.<destination>` adds to or overrides it for one host: the
// helper exports them at the start of each chat, shell, lazygit, yazi, and editor it opens
// there, in the same shell the session runs in, so the whole session inherits them. A
// terminal whose own name the host does not know is the case this is for -- `xterm-kitty` on
// a host with no kitty terminfo -- and the tracked setting starts at
// `ssh_env: TERM=xterm-256color` for every host. This machine's own sessions are untouched,
// and a read is left alone: only a session a reader works in is given the environment. The
// host is spelled exactly as zmx_servers spells it.
// The placement preference says where a launched terminal goes -- a tab, an OS window of its
// own, a split beside the current pane, or the pane the picker runs in, which is the default --
// and `ask` asks for it -- but only
// when a terminal is really needed: a session that a window already shows is switched to
// without a question, and a session with no window to switch to (a chat in a zmx session whose
// client was closed) is launched in the chosen place.
//
// Keys, in either list and in the preview (h and l move between the column and the preview,
// [ and ] walk every pane in turn). Only `? help` is shown in the footer; this list is
// available inside the picker. Each row shows the binding in force, so a rebound key is visible
// in the box:
//
//	{ , }      move to the previous or next target: this machine, then each host
//	alt+1 … 9  jump to the target the target bar numbers: 1 is this machine, then each
//	           configured host in the order zmx_servers lists them
//	tab        open or close the project group the cursor is in, which is the group of the
//	           session under a header
//	enter      open or close the selected project group; on a session, resume it; on a zmx row,
//	           attach; on a session from another host, resume it there over SSH
//	[ , ]      move the cursor to the previous or next pane: the project list, the zmx list
//	           under it, and the transcript preview when it is shown
//	h, l       move the cursor between the list column and the transcript preview
//	ctrl+a     a brand-new chat in the selected project, in the store in force
//	           (on another host, a new chat in that remote project)
//	ctrl+b     cycle the store a new chat uses, over the stores the target on screen has
//	           (the list itself shows every configured store at once)
//	ctrl+t     an extra kitty OS window; on a group header, open a shell there, and on a row
//	           that runs in a zmx session, attach or add a client
//	ctrl+x     a login shell in the project (on a host's row, on that host)
//	ctrl+g     lazygit at the top of the repository (on a host's row, on that host)
//	alt+f      yazi in the project directory (on a host's row, on that host)
//	alt+e      ask for a tab title, then open the project/session directory in $EDITOR
//	           (on a host's row, in that host's own editor: its $EDITOR, or nvim)
//	ctrl+f     fork a Pi session (pi --fork); on a session from another host, fork it there
//	           (a Claude Code session forks with --fork-session, a Codex one with codex fork,
//	           and OpenCode cannot fork)
//	ctrl+y     copy the resume command, or the zmx attach command; alt+y copies the transcript path
//	           (a session on another host copies the SSH command that opens it)
//	ctrl+v     show or hide the preview pane
//	j/k, ctrl+n/ctrl+p, ctrl+d/ctrl+u, pgup/pgdn, g/G, home/end   move or scroll
//	/          search the list that has the cursor: enter keeps the filter, esc clears
//	ctrl+e     rename a Pi session, through pi (a session on another host is left to that host,
//	           and an OpenCode, Claude Code, or Codex session cannot be renamed at all)
//	ctrl+w     delete a Pi transcript, here or on the host that holds it, after a question;
//	           on a project row it ignores or restores the project, and a zmx row ends the
//	           session it runs
//	alt+i      show the ignored projects instead of the active ones (this machine only)
//	alt+l      keep only the sessions a window already shows, and the zmx sessions a terminal
//	           is attached to (only this machine can answer for a session)
//	ctrl+l     cycle placement: tab, its own OS window, new pane, the picker's own pane, or ask
//	           each time
//	           a shell, lazygit, yazi, and the editor take the same preference on every list;
//	           ask offers the same pane too (4)
//	.          travel: the ways into the project space of the row under the cursor, which is what
//	           Cmd+. opens for a window outside the picker
//	alt+p      the command palette: the commands that are not travel, the delete interface first,
//	           each run the way its own key would run it
//	alt+d      delete the sessions older than an age: ask for the age, show the count, then
//	           delete (also the palette's first row)
//	ctrl+r     read the target on screen again, and reconnect when its master is gone
//	alt+r      end the SSH master for the target on screen and make a new one
//	?          the same list as a box, which q closes instead of quitting
//	q          quit
//
// Search and the name fields of the rename and fork prompts edit with Emacs keys: ctrl+a and
// ctrl+e go to the ends, ctrl+b and ctrl+f move by a character, alt+b and alt+f by a word,
// alt+backspace (or ctrl+w) and alt+d kill a word, ctrl+d deletes under the cursor, ctrl+k
// and ctrl+u kill to the end and to the start, and ctrl+y puts the last kill back. In search,
// enter keeps the filter and esc or ctrl+g clears it; in a name prompt, ctrl+g cancels like esc.
// These editing keys belong to the active field, so ctrl+w edits search text instead of running
// the list's project-ignore or delete action, and alt+f moves by a word instead of opening yazi.
//
// The helpers are scripts, so they are named with the interpreter that built this binary
// (see the build script in the chezmoi source): a kitty key-binding child has a minimal
// PATH, where /opt/homebrew/bin is absent.
package main
