#!/usr/bin/env python3
import uuid
try:
  import requests
except:
  print("Missing requests module.\nMitigation:\n\npython3 -mpip install --user requests")
  import sys
  sys.exit(1)

def uid(): return str(uuid.uuid4())

payload = {
  "initiator": {
    "name": "asdf",
    "email": "asdf@example.com",
    "notify": True,
    "timeZone": "Etc/GMT+12"
  },
  "participants": [],
  "comments": [],
  "options": [
    {
      "text": "1",
      "id": uid()
    },
    {
      "text": "2",
      "id": uid()
    },
    {
      "text": "4",
      "id": uid()
    },
    {
      "text": "5",
      "id": uid()
    }
  ],
  "type": "TEXT",
  "title": "asdf",
  "description": "",
  "timeZone": False,
  "preferencesType": "YESNO",
  "hidden": False,
  "remindInvitees": False,
  "askAddress": False,
  "askEmail": False,
  "askPhone": False,
  "rowConstraint": 1,
  "locale": "en_US"
}

for team in ('Cloud', 'MCM', 'I&S'):
  payload["title"] = team + " Team confidence vote"
  resp = requests.post("https://doodle.com/api/v2.0/polls", json=payload)
  if resp.status_code == 200:
    print(team)
    print("https://doodle.com/poll/" + resp.json()['id'])
