"""Our wee-slack patches, against the patched wee_slack.py with weechat stubbed.

Usage: wee-slack-patches.py <dir holding the patched wee_slack.py>

Queue (weechat/wee-slack-inflight.patch): requests the user waits on skip the
connect backlog. Drives the real EventRouter with weechat stubbed out: 40 background requests
fill the 16 slots and the waiting list and 20 more sit in the queue, then the buffer on screen asks for
history, /query opens a conversation and a websocket event arrives. The first
two must head the waiting list and the event must not sit in the queue
(weechat/wee-slack-inflight.patch, is_foreground).
"""

import os
import sys
import tempfile
import types
from unittest import mock

w = mock.MagicMock(WEECHAT_HOOK_PROCESS_RUNNING=-1)
w.current_buffer.return_value = "0xSCREEN"
sys.modules["weechat"] = w
sys.modules["websocket"] = mock.MagicMock()
os.environ["XDG_RUNTIME_DIR"] = tempfile.mkdtemp()
sys.path.insert(0, sys.argv[1])
import wee_slack  # noqa: E402

wee_slack.w = w  # bound under its __main__ guard, which an import skips
wee_slack.config = mock.MagicMock(debug_level=99, debug_mode=False)


def send(request, router):
    router.http_inflight += 1


wee_slack.local_process_async_slack_api_request = send
router = wee_slack.EventRouter()
team = mock.MagicMock(token="xoxc-x")


def request(channel, buffer=None, switch=False):
    return wee_slack.SlackRequest(
        team, "conversations.history", {"channel": channel},
        channel=types.SimpleNamespace(channel_buffer=buffer),
        metadata={"switch": True} if switch else {})


for i in range(40):
    router.receive(request("background%d" % i))
for _ in range(40):
    router.handle_next()
for i in range(20):  # still queued, not yet asking for a slot
    router.receive(request("queued%d" % i))
event = {"type": "message", "wee_slack_metadata_team": team}
router.receive(request("on-screen", buffer="0xSCREEN"))
router.receive(request("query", switch=True))
router.receive(event)
for _ in range(3):
    router.handle_next()

head = [r.post_data["channel"] for r in router.http_waiting[:2]]
failed = False

# Queued before its buffer was opened: once the user is on that buffer, it
# takes the next free slot ahead of older background requests.
waiting = [request("bg-a"), request("dm-opened-later", buffer="0xSCREEN"), request("bg-b")]
if wee_slack.next_waiting(waiting) != 1:
    print("a waiting request for the buffer on screen did not go next")
    failed = True
if wee_slack.next_waiting([request("bg-a"), request("bg-b")]) != 0:
    print("without a foreground request, waiting is not first-in first-out")
    failed = True

# The request log names the code path that asked.
def asks_history():
    return request("origin-probe")
if "asks_history" not in asks_history().origin:
    print("request origin missing its caller:", asks_history().origin)
    failed = True

# Unread probes (weechat/wee-slack-unreads.patch): once client.counts has
# answered, a DM or group DM it does not list gets no per-conversation probe.
def probes(listed, ok, call, starred=()):
    er = mock.MagicMock()
    team = types.SimpleNamespace(
        client_counts_ok=ok, client_counts_listed=listed, client_counts={}, token="xoxc-x",
        starred=set(starred),
        slack_api_translator={"mpim": {"info": "conversations.info"}})
    conv = types.SimpleNamespace(
        is_archived=False, is_open=False, unread_count_display=0, type="mpim",
        identifier="C9", team=team, eventrouter=er, got_history=False,
        channel_buffer="0xB", last_read=0, create_buffer=mock.MagicMock())
    call(conv, team, er)
    probes.opened = conv.create_buffer.called
    return er.receive.call_count
open_probe = lambda conv, team, er: wee_slack.SlackChannel.check_should_open(conv)
info_probe = lambda conv, team, er: wee_slack.handle_conversationsinfo(
    {"channel": {}}, er, team, conv, {})
for name, call in (("check_should_open", open_probe), ("conversations.info", info_probe)):
    counts = (probes(set(), True, call), probes({"C9"}, True, call), probes(set(), False, call))
    if counts != (0, 1, 1):
        print("%s probes (unlisted, listed, no counts) = %s, want (0, 1, 1)" % (name, counts))
        failed = True

# Starred DMs and group DMs get a buffer at connect, as the Slack apps list
# them, even with nothing unread.
if probes(set(), True, open_probe, starred={"C9"}) != 0 or not probes.opened:
    print("a starred group DM with nothing unread got no buffer")
    failed = True

# People for Ctrl+K (weechat/wee-slack-mentions.patch): everyone but you and
# deleted accounts, each with the team buffer /query has to run in.
people_team = types.SimpleNamespace(channel_buffer="0xTEAM", myidentifier="U0", users={
    "U0": types.SimpleNamespace(identifier="U0", name="me", deleted=False),
    "U1": types.SimpleNamespace(identifier="U1", name="adam.kozlowski", deleted=False),
    "U2": types.SimpleNamespace(identifier="U2", name="gone", deleted=True)})
wee_slack.EVENTROUTER = types.SimpleNamespace(teams={"T": people_team})
listed = wee_slack.slack_people_info_cb("", "slack_people", "")
wee_slack.EVENTROUTER = router
if listed != "0xTEAM\tadam.kozlowski":
    print("slack_people info:", repr(listed))
    failed = True

# Recency (weechat/wee-slack-recency.patch): the newest activity from any
# client, thread replies included, lands in the buffer localvar the buflist
# sorts on; every connect refetches it from client.counts.
def localvar_writes():
    return [c.args[2] for c in w.buffer_set.call_args_list
            if c.args[1] == "localvar_set_slack_latest"]


w.buffer_set.reset_mock()
conv = types.SimpleNamespace(channel_buffer="0xCONV", latest_activity="")
note = wee_slack.SlackChannel.note_activity
note(conv, "1700000002.000000")
note(conv, "1700000001.000000")  # older: ignored
if localvar_writes() != ["1700000002.000000"]:
    print("note_activity did not keep the newest ts:", localvar_writes())
    failed = True

seen = []
chan = mock.MagicMock()
chan.note_activity.side_effect = seen.append
for message in ({"ts": "1700000003.000000", "thread_ts": "1700000000.000000"},
                {"ts": "1700000004.000000", "subtype": "message_changed"}):
    try:
        wee_slack.process_message(message, router, team, chan, {})
    except Exception:
        pass  # the rest of message handling is not under test
events = open(os.path.join(os.environ["XDG_RUNTIME_DIR"], "wee-events.log")).read()
if " message " not in events or "ts=1700000003.000000" not in events:
    print("live messages missing from wee-events.log:", events)
    failed = True
if seen != ["1700000003.000000"]:
    print("process_message activity (thread reply counts, edit does not):", seen)
    failed = True

counted = types.SimpleNamespace(channel_buffer=None, latest_activity="")
counted.note_activity = lambda ts: note(counted, ts)
counts_team = mock.MagicMock(channels={"C1": counted})
wee_slack.handle_counts_latest(
    {"ok": True, "channels": [{"id": "C1", "latest": "1700000005.000000"}],
     "ims": [{"id": "D9", "latest": "1700000006.000000"}]},
    router, counts_team, None, {})
if counted.latest_activity != "1700000005.000000":
    print("client.counts latest not applied:", counted.latest_activity)
    failed = True

reconnecting = mock.MagicMock(channels={})
wee_slack.SlackTeam.set_connected(reconnecting)
sent = [c.args[0].request for c in reconnecting.eventrouter.receive.call_args_list]
if "client.counts" not in sent:
    print("connect did not refetch client.counts:", sent)
    failed = True

# Mentions (weechat/wee-slack-mentions.patch): @name completes from the whole
# workspace even where the channel's member list never loaded.
added = []
wee_slack.completion_list_add = lambda c, word, *a: added.append(word)
wee_slack.completion_get_string = lambda c, key: ""
users = {"U1": types.SimpleNamespace(name="adam.kozlowski", deleted=False),
         "U2": types.SimpleNamespace(name="gone", deleted=True)}
unloaded = types.SimpleNamespace(members=None, team=types.SimpleNamespace(
    users=users, members={"U1", "U2"}))
router.weechat_controller.buffers["0xCHAN"] = unloaded
wee_slack.EVENTROUTER = router
wee_slack.nick_completion_cb("", "", "0xCHAN", None)
if "@adam.kozlowski" not in added or "@gone" in added:
    print("@ completion in a channel without loaded members:", added)
    failed = True

# Threads (weechat/wee-slack-threads.patch): every connect asks for threads
# with unread replies, opens each and keeps it on the hotlist until read; a
# failed call is an ERROR line, which wee counts as a startup error.
if "subscriptions.thread.getView" not in sent:
    print("connect did not ask for unread threads:", sent)
    failed = True

w.prnt.reset_mock()
wee_slack.handle_threads_view({"ok": False, "error": "unknown_method"},
                              router, mock.MagicMock(domain="t"), None, {})
if not any(str(c.args[1]).startswith("ERROR") for c in w.prnt.call_args_list):
    print("a failed thread listing printed no ERROR line")
    failed = True

real_process_message = wee_slack.process_message
wee_slack.process_message = lambda *a, **k: None
root = mock.MagicMock(thread_channel=types.SimpleNamespace())
parent = mock.MagicMock()
parent.messages.get.return_value = root
wee_slack.handle_threads_view(
    {"ok": True, "threads": [
        {"root_msg": {"channel": "C1", "ts": "1700000000.000000"},
         "unread_replies": [{"ts": "1700000009.000000"}]},
        {"root_msg": {"channel": "C1", "ts": "1700000001.000000"},
         "unread_replies": []}]},
    router, mock.MagicMock(channels={"C1": parent}), None, {})
wee_slack.process_message = real_process_message
if root.open_thread.call_count != 1 or not root.thread_channel.unread_on_open:
    print("unread thread not opened and flagged:", root.open_thread.call_count)
    failed = True

# Read threads fill the view: no "missing" notice when every unread reply was
# listed, one when some were not.
def missing_notices(threads, total):
    teamx = mock.MagicMock(channels={}, channel_buffer="0xTEAM")
    w.prnt.reset_mock()
    wee_slack.handle_threads_view({"ok": True, "has_more": True, "threads": threads,
                                   "total_unread_replies": total}, router, teamx, None, {})
    return [c for c in w.prnt.call_args_list if c.args[0] == "0xTEAM"]
listed = [{"root_msg": {}, "unread_replies": [{}, {}]}] + [{"root_msg": {}, "unread_replies": []}] * 9
if missing_notices(listed, 2) or not missing_notices(listed, 5):
    print("missing-threads notice wrong: all listed=%d, 3 missing=%d"
          % (len(missing_notices(listed, 2)), len(missing_notices(listed, 5))))
    failed = True

w.buffer_set.reset_mock()
thread = mock.MagicMock(channel_buffer="0xTHREAD", unread_on_open=True,
                        visible_messages={}, pending_history_requests=set())
wee_slack.SlackChannelCommon.reprint_messages(thread)
if not any(c.args[1:] == ("hotlist", "1") for c in w.buffer_set.call_args_list):
    print("reprinting an unread thread dropped it from the hotlist")
    failed = True

ts = wee_slack.SlackTS
thread = mock.MagicMock(unread_on_open=True, parent_message=mock.MagicMock(
    subscribed=False, submessages=[ts("1700000001.000000"), ts("1700000002.000000")]))
wee_slack.SlackThreadChannel.mark_read(thread, ts="1700000001.000000")
kept = thread.unread_on_open
wee_slack.SlackThreadChannel.mark_read(thread, ts="1700000002.000000")
if not kept or thread.unread_on_open:
    print("unread flag: kept on old-line read=%s (want True), after newest=%s"
          " (want False)" % (kept, thread.unread_on_open))
    failed = True

# Upload (weechat/wee-slack-upload.patch): Slack retired files.upload, so
# /slack upload asks for an upload URL, sends the file there, then shares it
# with the comment after " -- " (the Ctrl+V caption).
picture = os.path.join(os.environ["XDG_RUNTIME_DIR"], "shot.png")
with open(picture, "wb") as f:
    f.write(b"12345")
wee_slack.EVENTROUTER = router
wee_slack.ProxyWrapper = lambda: types.SimpleNamespace(curl=lambda: "")
dm = mock.MagicMock(type="im", identifier="D1", team=team)
router.weechat_controller.buffers["0xDM"] = dm
w.info_get.return_value = ""
w.hook_process_hashtable.reset_mock()
wee_slack.command_upload(None, "0xDM", picture + " -- look at this")
def last_curl():
    args, data = w.hook_process_hashtable.call_args.args[1], w.hook_process_hashtable.call_args.args[4]
    return [args[k] for k in sorted(args, key=lambda k: int(k[3:]))], data
argv, data = last_curl()
steps = [argv[-1].split("?")[0].rsplit("/", 1)[-1]]
wee_slack.upload_callback(data, "", 0, '{"ok": true, "upload_url": "https://files.example/u", "file_id": "F1"}', "")
argv, data = last_curl()
steps.append(argv[-1])
sent_file = "-Ffile=@" + picture in argv
wee_slack.upload_callback(data, "", 0, "200", "")
argv, data = last_curl()
steps.append(argv[-1].split("?")[0].rsplit("/", 1)[-1])
w.prnt.reset_mock()
wee_slack.upload_callback(data, "", 0, '{"ok": true}', "")
if steps != ["files.getUploadURLExternal", "https://files.example/u",
             "files.completeUploadExternal"] or not sent_file:
    print("upload steps wrong:", steps, "file sent:", sent_file)
    failed = True
if "look+at+this" not in argv[-1] or "D1" not in argv[-1] or "F1" not in argv[-1]:
    print("shared upload lacks comment, channel or file id:", argv[-1].split("?", 1)[-1])
    failed = True
if w.prnt.call_args_list:
    print("a successful upload printed:", w.prnt.call_args_list)
    failed = True

# Two pasted images and an empty message: weechat trims the trailing space,
# leaving "--" after the paths; both files go out in one message, no text.
other = picture.replace("shot", "shot2")
with open(other, "wb") as f:
    f.write(b"678")
w.hook_process_hashtable.reset_mock()
w.prnt.reset_mock()
wee_slack.command_upload(None, "0xDM", picture + " " + other + " --")
calls = []
for i, reply in enumerate(['{"ok": true, "upload_url": "https://files.example/1", "file_id": "F1"}', "200",
                           '{"ok": true, "upload_url": "https://files.example/2", "file_id": "F2"}', "200"]):
    argv, data = last_curl()
    calls.append(argv)
    wee_slack.upload_callback(data, "", 0, reply, "")
argv, data = last_curl()
if (w.prnt.call_args_list or "-Ffile=@" + other not in calls[3]
        or "F1" not in argv[-1] or "F2" not in argv[-1] or "initial_comment" in argv[-1]):
    print("two files with no message:", w.prnt.call_args_list, argv[-1].split("?", 1)[-1])
    failed = True
wee_slack.upload_callback(data, "", 0, '{"ok": false, "error": "not_allowed"}', "")
if not any("not_allowed" in str(c) for c in w.prnt.call_args_list):
    print("a failed upload printed no error")
    failed = True

# Download (weechat/wee-slack-download.patch): the token and cookie only go
# to files.slack.com, redirects unfollowed; done or failed, a signal says so.
w.hook_process_hashtable.reset_mock()
w.prnt.reset_mock()
for bad in ("https://evil.example/x.png /tmp/x", "https://files.slack.com.evil.example/x /tmp/x",
            "https://files.slack.com/files-pri/T-F/x.png"):
    wee_slack.command_download(None, "0xDM", bad)
if w.hook_process_hashtable.called or w.prnt.call_count != 3:
    print("download did not refuse a non-Slack URL or a missing path")
    failed = True
wee_slack.command_download(None, "0xDM", "https://files.slack.com/files-pri/T-F/x.png /run/v/0-x.png")
argv, path = last_curl()
if (argv[-2:] != ["/run/v/0-x.png", "https://files.slack.com/files-pri/T-F/x.png"]
        or any("L" in a for a in argv if a.startswith("-s"))
        or not any("Bearer xoxc-x" in a for a in argv)):
    print("download curl args wrong:", [a for a in argv if "xoxc" not in a])
    failed = True
w.hook_signal_send.reset_mock()
wee_slack.download_callback(path, "", 0, "", "")
wee_slack.download_callback(path, "", 22, "", "")
sent = [c.args[2] for c in w.hook_signal_send.call_args_list if c.args[0] == "slack_download_done"]
if sent != ["/run/v/0-x.png", ""]:
    print("download signals wrong:", sent)
    failed = True

# Links (weechat/wee-slack-openlink.patch): a Slack message link shows its
# conversation, or its thread, and signals which message to land on; the
# root of a thread older than the loaded history is fetched first.
def link_signals():
    return [c.args[2] for c in w.hook_signal_send.call_args_list
            if c.args[0] == "slack_link_opened"]


link_er = mock.MagicMock()
link_team = types.SimpleNamespace(domain="matchi.slack.com", eventrouter=link_er, channels={},
                                  token="xoxc-x")
thread_buffer = types.SimpleNamespace(channel_buffer="0xTH")
root_message = types.SimpleNamespace(thread_channel=thread_buffer, open_thread=mock.MagicMock())
link_channel = mock.MagicMock(channel_buffer="0xC1", identifier="C1", messages={})
link_team.channels["C1"] = link_channel
link_controller = types.SimpleNamespace(buffers={})
wee_slack.EVENTROUTER = types.SimpleNamespace(teams={"T": link_team}, weechat_controller=link_controller)
w.hook_signal_send.reset_mock()
w.prnt.reset_mock()
wee_slack.command_openlink(None, "", "https://matchi.slack.com/archives/C1/p1700000000123456")
wee_slack.command_openlink(None, "", "https://other.slack.com/archives/C1/p1700000000123456")
wee_slack.command_openlink(None, "", "https://matchi.slack.com/archives/C9/p1700000000123456")
if link_signals() != ["0xC1 1700000000.123456"] or w.prnt.call_count != 2:
    print("openlink of a channel message:", link_signals(), w.prnt.call_args_list)
    failed = True
reply = "https://matchi.slack.com/archives/C1/p1700000500000002?thread_ts=1700000000.000001&cid=C1"
w.hook_signal_send.reset_mock()
wee_slack.command_openlink(None, "", reply)
fetch = link_er.receive.call_args.args[0] if link_er.receive.called else None
if link_signals() or fetch is None or fetch.post_data.get("latest") != "1700000000.000001":
    print("a reply whose root is not loaded did not fetch the root:", link_signals(), fetch)
    failed = True
else:
    def stores_root(message, er, team, channel, metadata, history_message=False):
        channel.messages[wee_slack.SlackTS(message["ts"])] = root_message
    with mock.patch.object(wee_slack, "process_message", stores_root):
        wee_slack.handle_openlink_root({"ok": True, "messages": [{"ts": "1700000000.000001"}]},
                                       link_er, link_team, link_channel, fetch.metadata)
    if link_signals() != ["0xTH 1700000500.000002"] or not root_message.open_thread.called:
        print("openlink of a thread reply:", link_signals())
        failed = True
# Now loaded: the same link opens the thread without asking Slack again.
link_er.receive.reset_mock()
w.hook_signal_send.reset_mock()
wee_slack.command_openlink(None, "", reply)
if link_er.receive.called or link_signals() != ["0xTH 1700000500.000002"]:
    print("a reply whose root is loaded:", link_er.receive.called, link_signals())
    failed = True
# A conversation without a buffer gets one, without joining it.
unopened = mock.MagicMock(channel_buffer=None, identifier="C2", messages={})
unopened.open.side_effect = lambda update_remote: setattr(unopened, "channel_buffer", "0xC2")
link_team.channels["C2"] = unopened
w.hook_signal_send.reset_mock()
wee_slack.command_openlink(None, "", "https://matchi.slack.com/archives/C2/p1700000000123456")
if unopened.open.call_args != mock.call(update_remote=False) or link_signals() != ["0xC2 1700000000.123456"]:
    print("openlink of an unopened conversation:", unopened.open.call_args, link_signals())
    failed = True

# /slack parent: from a thread back to the message it started from.
thread = mock.MagicMock(spec=wee_slack.SlackThreadChannel)
thread.parent_channel = link_channel
thread.thread_ts = "1700000000.000001"
link_controller.buffers.update({"0xTH": thread, "0xC1": link_channel})
w.hook_signal_send.reset_mock()
w.buffer_close.reset_mock()
w.prnt.reset_mock()
wee_slack.command_parent(None, "0xC1", "")
wee_slack.command_parent(None, "0xTH", "")
if (link_signals() != ["0xC1 1700000000.000001"] or w.buffer_close.call_args != mock.call("0xTH")
        or w.prnt.call_count != 1):
    print("/slack parent:", link_signals(), w.buffer_close.call_args, w.prnt.call_args_list)
    failed = True
wee_slack.EVENTROUTER = router

# Edit (weechat/wee-slack-edit.patch): one of your messages goes into the
# input as you would type it; Enter sends it back as chat.update.
edit_team = mock.MagicMock(myidentifier="U0", token="xoxc-x",
                           users={"U1": types.SimpleNamespace(name="adam")},
                           channels={"C2": types.SimpleNamespace(name="dev")})
edit_team.get_username_map.return_value = {"adam": "U1"}
edit_team.get_channel_map.return_value = {"dev": "C2"}
edit_team.generate_usergroup_map.return_value = {}
mine = types.SimpleNamespace(ts=wee_slack.SlackTS("5.000001"), user_identifier="U0", message_json={
    "text": "hi <@U1> in <#C2|dev> <https://a.example|label> &amp; <!here>"})
theirs = types.SimpleNamespace(ts=wee_slack.SlackTS("6.000001"), user_identifier="U1",
                               message_json={"text": "not yours"})
conv = mock.MagicMock(spec=wee_slack.SlackChannel)
conv.configure_mock(team=edit_team, identifier="C1", editing=None,
                    messages={mine.ts: mine, theirs.ts: theirs})
edit_controller = mock.MagicMock()
edit_controller.buffers = {"0xE": conv}
edit_controller.get_channel_from_buffer_ptr.return_value = conv
wee_slack.EVENTROUTER = types.SimpleNamespace(weechat_controller=edit_controller)
wee_slack.weechat_version = 0x4000000  # set under the __main__ guard too
wee_slack.config.map_underline_to = "_"
w.buffer_set.reset_mock()
wee_slack.command_edit(None, "0xE", "6.000001")
refused = conv.editing is None and not w.buffer_set.called
wee_slack.command_edit(None, "0xE", "5.000001")
inputs = [c.args[2] for c in w.buffer_set.call_args_list if c.args[1] == "input"]
if not refused or inputs != ["hi @adam in #dev https://a.example & @here"]:
    print("/slack edit: refused someone else's=%s, input=%s" % (refused, inputs))
    failed = True
conv.eventrouter = mock.MagicMock()
wee_slack.buffer_input_callback("EVENTROUTER", "0xE", "hi @adam, fixed")
update = conv.eventrouter.receive.call_args.args[0] if conv.eventrouter.receive.called else None
if (update is None or update.request != "chat.update" or update.post_data["ts"] != "5.000001"
        or update.post_data["text"] != "hi <@U1>, fixed" or conv.editing is not None):
    print("Enter while editing:", update and (update.request, update.post_data), conv.editing)
    failed = True
wee_slack.EVENTROUTER = router

# Reactions (weechat/wee-slack-reactions.patch): adding one that is already
# there says nothing; any other failure still does.
w.prnt.reset_mock()
wee_slack.handle_reactionsadd({"ok": False, "error": "already_reacted"}, None, None, None,
                              {"reaction": "eyes"})
quiet = not w.prnt.called
wee_slack.handle_reactionsadd({"ok": False, "error": "invalid_name"}, None, None, None,
                              {"reaction": "eyes"})
if not quiet or not w.prnt.called:
    print("reactions.add errors: already_reacted quiet=%s, others shown=%s" % (quiet, w.prnt.called))
    failed = True

if head != ["query", "on-screen"]:
    print("foreground requests not at the head of the waiting list:", head)
    failed = True
if any(item is event for item in router.queue):
    print("websocket event still queued behind the backlog")
    failed = True
sys.exit(1 if failed else 0)
