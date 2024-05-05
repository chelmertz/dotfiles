#!/usr/bin/env python3

import sys
from xml.sax.saxutils import quoteattr

print("""<opml version="1.0">
<body>
""")
for line in sys.stdin.readlines():
    if 'http' in line:
        url = line.split(' ')[1].strip()
        attr = quoteattr(url)
        print("<outline text={} xmlUrl={}/>".format(attr, attr))

    
# this expects something like
# cat ~/Dropbox/orgzly/feeds.org
# as input, where one line can be something like
# *** http://asdf.com/feed

print(
    """</body>
</opml>
""")
