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
| Ctrl+K | buffer picker (type to filter, Enter picks, Ctrl+K again cancels) |
| Alt+A | next buffer with unread activity |
| Alt+/ | back to the previous buffer (again: forward) |
| Alt+< / Alt+> | back / forward through visited buffers |
| `/all` | toggle the right-hand list between active-only and everything |
| `/query name<Tab>` | open a DM with anyone in the workspace |
| `/close` | close the current buffer (back to the previous one) |

The right-hand list shows the current buffer, buffers with new messages, and
your Slack-starred conversations. Muted channels count as quiet. Both
workspaces (MATCHi, op6) appear, grouped by workspace.

## Writing

- Enter sends. **Shift+Enter** or **Alt+Enter** inserts a newline.
- **Ctrl+X** opens the message in nvim; save and quit to put it back, then Enter.
- `+:emoji:` reacts to the last message (`-:emoji:` removes); `3+:emoji:` the
  third-to-last. `:smi<Tab>` completes emoji names.

## Threads

- A message with replies ends in `[ Thread: 855 Replies: 8 ]`; that thread is
  **`$855`**. A bare number counts messages back from the end instead.
- **`/cursor`**, arrow keys to a message, then **T** opens its thread (even
  with no replies yet) or **R** prefills `/reply $id `. `q` leaves cursor mode.
  This is the race-free way: it targets that exact message.
- `/thread` (last message), `/thread 3` (third-to-last), `/thread $855`.
- `/reply text`, `/reply 3 text`, `/reply $855 text`; `-alsochannel` also
  posts to the channel. In a thread buffer, just type.

## Links

- URLs carry small numbers (¹ newest, ² before it). **`/u 1`** opens the newest
  in the browser, `/u 2` the one before.
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
