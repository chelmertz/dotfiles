# wee: Slack in weechat

`wee` runs Slack inside weechat through the wee-slack script. It replaces the
Electron Slack app for daily use; the app stays installed for huddles.

## Start and stop

- **F9** raises the wee window (ghostty, class `org.weechat.wee`, workspace 9),
  starting it if needed; pressing it again returns to where you were.
- **`$mod+q`** quits it like any window; weechat saves its config on exit.
- Only one weechat can use the config at a time: quit before starting another.
- A red **LOADING n/m** chip leads the bottom bar while workspaces connect and
  while weechat is too busy to take keys (tens of seconds after a cold start).
  Type once it is gone.

## Moving around

| Key / command | Does |
|---|---|
| Ctrl+K | buffer picker, then everyone in the workspaces (type to filter, Enter picks: a person opens the DM; Esc cancels) |
| Alt+A | next buffer with unread activity; after the last, back to where you started; "nothing unread" in the bar when there is nowhere to go |
| Alt+/ | back to the previous buffer (again: forward) |
| Alt+← / Alt+→ (or Alt+< / Alt+>) | back / forward through visited buffers |
| `/all` | toggle the right-hand list between active-only and everything |
| `/query name<Tab>` | open a DM with anyone in the workspace |
| `/query alice bob` | open a group DM (Tab completes each name; commas work too) |
| `/close` | close the current buffer (back to the previous one) |

The right-hand list shows the current buffer, buffers with new messages, your
Slack-starred conversations, and anything with activity in the last 72 hours
(read or not, from any client), newest first. Muted channels count as quiet.
`/all` lists everything.

Name prefixes, in the list and in Ctrl+K: `#` channel, 🔒 private channel,
`%` Slack Connect channel (shared with another company), `@` group DM, `+`
DM with someone online, plain name a DM (or, in Ctrl+K, a person to start one
with), `>` someone is typing there.

## Writing

- Enter sends. **Shift+Enter** or **Alt+Enter** inserts a newline.
- **Ctrl+X** opens the message in nvim; save and quit to put it back, then Enter.
- `@name<Tab>` completes anyone in the workspace.
- **Ctrl+V** with an image on the clipboard attaches it, previewed in a
  floating feh window; the input line shows `[📎 1 image]`. Each Ctrl+V adds
  one more. Esc or Enter first closes the preview; then Enter sends what you
  typed as one message with the images, and Esc (or switching buffer) drops
  them.
  Without an image, Ctrl+V pastes the text. `/slack upload <file>… [-- text]`
  sends files from disk.
- `+:emoji:` reacts to the last message (`-:emoji:` removes); `3+:emoji:` the
  third-to-last. `:smi<Tab>` completes emoji names.

## Message menu

- **Shift+Space** (or Alt+Space) on an empty input line opens a menu in the
  bottom bar for one message, previewed at its top: the newest, or step with
  **k** (older) and **j** (newer). With text typed it is just a space.
- **r** reply in thread (works with no thread yet), **t** open thread,
  **e** react (prefills `$id+:`), **y** copy with date and author,
  **Y** copy every message in the buffer (the whole thread, in a thread
  buffer), **l** copy the permalink, **o** open its link (with several, the
  bar numbers them and a digit picks), **v** view its images in a floating
  feh (arrow keys step through several; Esc or Enter in wee closes it).
- **q** or **Esc** closes the menu; any other key closes it and does what it
  normally does. Esc also cancels Ctrl+K. (The wee window's ghostty sends Esc
  as a code weechat can tell apart from Alt+key; elsewhere in weechat a bare
  Esc merges with the next key.)

## Threads

- Threads you follow with unread replies, including replies from while wee was
  closed, open as their own buffers at start and are on Alt+A's way; leaving
  one marks it read in Slack. A reply arriving while wee runs is announced in
  the workspace buffer (Alt+A stops there). If wee cannot list unread threads,
  that is a startup error: check the Slack app's Threads view.

- A message with replies ends in `[ Thread: 855 Replies: 8 ]`; that thread is
  **`$855`**. A bare number counts messages back from the end instead.
- **`/cursor`**, arrow keys to a message, then **T** opens its thread (even
  with no replies yet) or **R** prefills `/reply $id `. `q` leaves cursor mode.
  This is the race-free way: it targets that exact message.
- `/rehistory -remote` reloads the current buffer from Slack, for a message
  that should be there but isn't.
- `/thread` (newest thread), `/thread 3` (third-newest thread), `/thread $855`.
- `/reply text`, `/reply 3 text`, `/reply $855 text`; `-alsochannel` also
  posts to the channel. In a thread buffer, just type.

## Links

- Links in messages that arrived while wee ran carry small numbers (¹ newest,
  ² before it); **`/u 1`** opens the newest, `/u 2` the one before. Links in
  older messages (loaded history) have none: use the message menu's **o**.
- Or **Ctrl+click** a URL. If it wraps onto an indented line, press **Alt+L**
  (bare view) first; any key returns.

## Read state

- A buffer is marked read in Slack when you **leave** it, up to what had
  loaded. Quitting marks nothing, so the buffer open at quit shows unread
  again on the next start.
- Unread state at start comes from Slack's own badge counts, so what you read
  on the phone or desktop app is read here too. While wee runs, wee-slack
  handles Slack's live "marked read" events too (from the code; not yet
  observed in practice).

## Sessions

`slack-session-ensure` (run by `wee` in the background) keeps one token per
workspace valid, taking fresh ones from the Slack desktop app's local data. If
it says a session expired, open the Slack desktop app once, then
`/python reload slack`. New workspaces you sign into in the desktop app are
picked up on the next start.

## Where it lives

`bin/wee` (entrypoint and the source of truth for wee's settings),
`bin/slack-session-ensure`, `weechat/slack_hints.py` (bottom bar),
`weechat/wee-slack-unreads.patch` (unread and starred fixes on top of
wee-slack), the weechat block in `nix/home.nix`, F9 in `.i3/config`.
