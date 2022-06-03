#!/usr/bin/env python3

import json
import subprocess

out = subprocess.check_output(['zenity', '--text-info', '--editable'])
print(json.dumps(json.loads(out), indent=4))
