#!/usr/bin/env python3
# encoding: utf-8
import logging
import os
import subprocess
import sys

"""
Usage:

./glmr.py
./glmr.py i3blocks
./glmr.py i3blocks awesome
./glmr.py debug

Configure i3blocks with:

[gitlabmr]
interval=300
command=~/bin/glmr.py i3blocks awesome
instance=~/file-with-only-your-gitlab-token
"""

# gitlab API docs for the merge request endpoint:
# https://docs.gitlab.com/ee/api/merge_requests.html#list-merge-requests

try:
    import requests
except:
    sys.stderr.write("""You need to install requests:
python3 -mpip install --user requests""")

log = logging.getLogger(__name__)
log.addHandler(logging.StreamHandler(sys.stderr))

api_host='https://gitlab.elvaco.se'
mr_url='{}/api/v4/merge_requests'.format(api_host)

missing_token="""Missing GITLAB_TOKEN

Go to Gitlab, create a personal token with API rights.

Copy the next line to ~/.bashrc
export GITLAB_TOKEN="token-from-gitlabs-gui"

and then type this in your terminal:
source ~/.bashrc

and execute this script again.
"""

def ansi_red(string):
    """See https://en.wikipedia.org/wiki/ANSI_escape_code"""
    red='\033[1;31m'
    reset='\033[0;0m'
    return red + str(string) + reset

def ansi_cyan(string):
    """See https://en.wikipedia.org/wiki/ANSI_escape_code"""
    cyan='\033[1;96m'
    reset='\033[0;0m'
    return cyan + str(string) + reset

def ansi_green(string):
    """See https://en.wikipedia.org/wiki/ANSI_escape_code"""
    green='\033[1;32m'
    reset='\033[0;0m'
    return green + str(string) + reset

def color_projectname(url):
    parts=url.split('/')
    parts[4]=ansi_red(parts[4])
    return '/'.join(parts)

separator = '\n' * 2 + '=' * 10 + '\n' * 2

def username_for_token(bearer_token):
    headers={
        'authorization': 'Bearer ' + bearer_token,
        'content-type': 'application/json',
    }

    graphql_url='{}/api/graphql'.format(api_host)
    import json
    data=json.dumps({'query': 'query {currentUser {name}}'})
    username = requests.post(graphql_url, data=data, headers=headers)
    username.raise_for_status()
    return username.json()['data']['currentUser']['name']

def review_needed(bearer_token):
    params_others={
        'with_merge_status_recheck': 'true',
        'state': 'opened',
        'wip': 'no',
    }
    headers={
        'authorization': 'Bearer ' + bearer_token
    }

    review_plz = requests.get(mr_url, params=params_others, headers=headers)
    review_plz.raise_for_status()

    my_username = username_for_token(bearer_token)

    out=[]
    urls=set()

    # TODO hide if I already upvoted
    for mr in review_plz.json():
        url=mr['web_url']

        if mr['author']['name'] == my_username:
            log.debug('Skipping, created by me: ' + url)
            continue

        if mr['merge_status'] != 'can_be_merged':
            log.debug("Cannot be merged: " + url)
            continue

        tasks_left = mr['task_completion_status']['count'] - mr['task_completion_status']['completed_count']
        if tasks_left > 0:
            log.debug('Still unsolved tasks: ' + url)
            continue

        if mr['upvotes'] > 0:
            log.debug('Already has upvotes: ' + url)
            continue

        out.append('{}\n{}\n{}'.format(
            ansi_red(mr['title']),
            mr['author']['name'],
            color_projectname(url)
        ))
        urls.add(url)

    return len(urls), (separator).join(out), urls

def my_mrs_needing_attention(bearer_token):
    """MRs that only needs people looking on them, should be triggerable."""
    params_mine={
        'with_merge_status_recheck': 'true',
        'scope': 'created_by_me',
        'state': 'opened',
    }
    headers={
        'authorization': 'Bearer ' + bearer_token
    }

    needs_attn = requests.get(mr_url, params=params_mine, headers=headers)
    needs_attn.raise_for_status()

    out=[]
    urls=set()

    for mr in needs_attn.json():
        todos=[]
        url=mr['web_url']
        interesting=False

        if mr['merge_when_pipeline_succeeds']:
            log.debug('Merging when pipeline succeeds: ' + url)
            continue

        if mr['merge_status'] != 'can_be_merged':
            todos.append("Cannot be merged")

        tasks_left = mr['task_completion_status']['count'] - mr['task_completion_status']['completed_count']
        if tasks_left > 0:
            todos.append('Unsolved tasks: ' + tasks_left)

        if mr['work_in_progress'] == 'true':
            todos.append('WIP')

        if mr['has_conflicts'] == 'true':
            todos.append('Has conflicts')

        if mr['downvotes'] > 0:
            todos.append('Downvotes: {}'.format(mr['downvotes']))

        # TODO this could be tweaked.. 1+ upvotes = "gogo merge"? 0 upvotes = "gogo spam invites"?
        todos.append('{} upvotes'.format(mr['upvotes']))

        if len(todos) > 0:
            urls.add(url)
            out.append('{}\n{}\n{}'.format(
                ansi_red(mr['title']),
                '- ' + '\n- '.join(todos),
                color_projectname(url)
            ))

    return len(urls), (separator).join(out), urls

def get_token(env):
    token=env.get('GITLAB_TOKEN')
    if token is not None:
        return token

    # BLOCK_INSTANCE comes from i3blocks
    token_file=env.get('BLOCK_INSTANCE')
    if token_file is None:
        return None

    try:
        with open(os.path.expanduser(token_file)) as f:
            return f.read().strip()
    except:
        return None

if __name__ == '__main__':
    token=get_token(os.environ)
    if token is None:
        sys.stderr.write(missing_token)
        exit(1)

    if 'debug' in sys.argv:
        log.setLevel(logging.DEBUG)

    count_others, others, other_urls = review_needed(token)
    count_yours, yours, your_urls = my_mrs_needing_attention(token)

    if 'i3blocks' in sys.argv:
        if os.environ.get('BLOCK_BUTTON') == '1':
            #  xdg-open opens *all* urls that needs attention, on left-click in i3blocks
            for u in other_urls | your_urls:
                subprocess.call(['xdg-open', u])
        if 'awesome' in sys.argv:
            # https://fontawesome.com/icons/code-branch?style=solid
            # https://fontawesome.com/icons/magic?style=solid
            labels = '', ''
        else:
            labels = 'others', 'yours'

        summary="{} {} {} {}".format(labels[0], count_others, labels[1], count_yours)
        print(summary)
        if count_others + count_yours > 0:
            # i3blocks lines logic: https://vivien.github.io/i3blocks/#_format
            print(summary)
            print('#00ff00')
    else:
        print(ansi_cyan('NEEDS REVIEW ({})'.format(count_others)))
        print(others)
        print(ansi_cyan('YOUR MRS ({})'.format(count_yours)))
        print(yours)
