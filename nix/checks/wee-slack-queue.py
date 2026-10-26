"""Requests the user waits on skip wee-slack's connect backlog.

Usage: wee-slack-queue.py <dir holding the patched wee_slack.py>

Drives the real EventRouter with weechat stubbed out: 40 background requests
fill the 16 slots and the waiting list and 20 more sit in the queue, then the buffer on screen asks for
history, /query opens a conversation and a websocket event arrives. The first
two must head the waiting list and the event must not sit in the queue
(weechat/wee-slack-inflight.patch, is_foreground).
"""

import sys
import types
from unittest import mock

w = mock.MagicMock(WEECHAT_HOOK_PROCESS_RUNNING=-1)
w.current_buffer.return_value = "0xSCREEN"
sys.modules["weechat"] = w
sys.modules["websocket"] = mock.MagicMock()
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
if head != ["query", "on-screen"]:
    print("foreground requests not at the head of the waiting list:", head)
    failed = True
if any(item is event for item in router.queue):
    print("websocket event still queued behind the backlog")
    failed = True
sys.exit(1 if failed else 0)
