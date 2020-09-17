#!/usr/bin/env python3
# encoding: utf-8
"""
Gitlab merge request status script

Usage:

./glmr.py
./glmr.py i3blocks
./glmr.py i3blocks awesome
./glmr.py debug

Configure i3blocks with:

[gitlabmr]
interval=300
command=~/bin/glmr.py i3blocks awesome
instance=~/.gitlab-token
"""

import json
import logging
import os
import subprocess
import sys
import urllib

# gitlab API docs for the merge request endpoint:
# https://docs.gitlab.com/ee/api/merge_requests.html#list-merge-requests

try:
    import requests
except:
    sys.stderr.write("""You need to install requests:
python3 -mpip install --user requests""")

log = logging.getLogger(__name__)
log.addHandler(logging.StreamHandler(sys.stderr))

lockfile=os.path.expanduser('~/.glmr_onoff')

default_token_path='~/.gitlab-token'

# TODO this should be configurable
api_host='https://gitlab.elvaco.se'
mr_url='{}/api/v4/merge_requests'.format(api_host)
notes_url='{}/api/v4/projects/{}/merge_requests/{}/notes'

missing_token='''Missing gitlab token

Go to Gitlab, create a personal token with API rights.

Copy the token to {} and run this script again.

Advanced:

Copy the token to any file, and enter the following line into ~/.bashrc:

export GITLAB_TOKEN="~/path-to-file-with-gitlab-token"

Then type this in your terminal:
source ~/.bashrc

and execute this script again.
'''.format(default_token_path)

no_connection='''Could not connect to gitlab ({}),
please check the URL, your internet connection and/or VPN'''.format(api_host)

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
    data=json.dumps({'query': 'query {currentUser {name}}'})
    username = requests.post(graphql_url, data=data, headers=headers)
    username.raise_for_status()
    currentUser=username.json()['data']['currentUser']
    assert currentUser != None, "Most probably not a valid token, double check its expiration date"
    return currentUser['name']

def projects_starred_by_current_user(bearer_token):
    params_others={
        'starred': 'true',

        # we only need a single field, minimize payload
        'simple': 'true',
    }
    headers={
        'authorization': 'Bearer ' + bearer_token
    }

    starred_url='{}/api/v4/projects'.format(api_host)
    starred = requests.get(starred_url, params=params_others, headers=headers)
    starred.raise_for_status()

    project_ids=set(map(lambda x: x['id'], starred.json()))
    log.debug('Starred projects: {}'.format(project_ids))
    return project_ids

def unanswered_mr_notes_by_me(bearer_token, my_username, project_name, mr_id):
    headers={
        'authorization': 'Bearer ' + bearer_token
    }
    
    # '/' is considered safe by default, we want it encoded though
    url=notes_url.format(api_host, urllib.parse.quote(project_name, safe=''), mr_id)
    notes=requests.get(url, headers=headers)
    notes.raise_for_status()

    decoded=notes.json()

    my_unresolved_notes=list(filter(lambda x:
                       x['author']['name'] == my_username
                       and x['resolvable'] == True
                       and x['resolved'] == False,
                       decoded))

    return len(my_unresolved_notes)

def review_needed(my_username, project_id_allowlist, bearer_token):
    params_others={
        # try to get the newest status of the MR
        'with_merge_status_recheck': 'true',

        # not merged
        'state': 'opened',

        'wip': 'no',

        # not only mine
        'scope': 'all',
    }
    headers={
        'authorization': 'Bearer ' + bearer_token
    }

    review_plz = requests.get(mr_url, params=params_others, headers=headers)
    review_plz.raise_for_status()

    out=[]
    urls=set()

    def skip(mr_url):
        def x(reason):
            log.debug('Skipping, {}: {}'.format(reason, mr_url))
        return x

    # TODO if interactive, and no config exists, ask for needed info and write it to a file

    # TODO hide if I already upvoted
    # TODO hide if I already commented, and that comment is not resolved
    for mr in review_plz.json():
        url=mr['web_url']
        skipper=skip(url)


        if mr['project_id'] not in project_id_allowlist:
            # I couldn't find an easier way to filter out relevant projects
            skipper('irrelevant project')
            continue

        if mr['author']['name'] == my_username:
            skipper('created by me')
            continue

        if mr['merge_status'] != 'can_be_merged':
            skipper('cannot be merged')
            continue

        tasks_left = mr['task_completion_status']['count'] - mr['task_completion_status']['completed_count']
        if tasks_left > 0:
            skipper('still unsolved tasks')
            continue

        if mr['upvotes'] > 0:
            skipper('already has upvotes')
            continue

        # expensive check, do this last
        project_name=mr['references']['full'].split('!')[0]
        unresolved_comments=unanswered_mr_notes_by_me(bearer_token, my_username, project_name, mr['iid']) 
        if unresolved_comments > 0:
            skipper('my comments are unresolved ({})'.format(unresolved_comments))
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

    def skip(mr_url):
        def x(reason):
            log.debug('Skipping, {}: {}'.format(reason, mr_url))
        return x

    for mr in needs_attn.json():
        todos=[]
        url=mr['web_url']
        interesting=False
        skipper=skip(url)

        if mr['merge_when_pipeline_succeeds']:
            skipper('merging when pipeline succeeds')
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

        # TODO the newer gitlab version seems to use "approval" instead (or complimentary?) to upvotes
        # TODO this could be tweaked.. 1+ upvotes = "gogo merge"? 0 upvotes = "gogo spam invites"?
        # https://fontawesome.com/icons/volume-up?style=solid  (speaker icon) for separating OK mrs (len(todos) == 0) from "needs work"
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
    # BLOCK_INSTANCE comes from i3blocks
    token_file=env.get('GITLAB_TOKEN', env.get('BLOCK_INSTANCE', default_token_path))
    if token_file is None:
        return None

    try:
        with open(os.path.expanduser(token_file)) as f:
            token=f.read().strip()
            assert token != "", "Token must be non-empty, please check the file {}".format(token_file)
            return token
    except:
        return None

def printer():
    def x(string):
        print("{}".format(string))
    return x

def glmr_is_toggled_off():
    try:
        open(lockfile, 'r').read()
        return True
    except:
        return False
        
def toggle_glmr():
    if glmr_is_toggled_off():
        os.remove(lockfile)
    else:
        open(lockfile, 'a')

if __name__ == '__main__':
    i3blocks = '--i3blocks' in sys.argv
    awesome = '--awesome' in sys.argv

    p=printer()

    if os.environ.get('BLOCK_BUTTON') == '3':
        toggle_glmr()

    if i3blocks and glmr_is_toggled_off():
        #https://fontawesome.com/icons/toggle-off?style=solid
        p('{} (right click to activate)'.format('' if awesome else 'disconnected', api_host))
        exit(0)
    
    # TODO place config etc in a single ini file (configparser), and replace all sys.argv shit with argparse
    token=get_token(os.environ)
    if token is None:
        sys.stderr.write(missing_token)
        exit(1)

    if 'debug' in sys.argv:
        log.setLevel(logging.DEBUG)

    try:
        count_others, others, other_urls = review_needed(
            username_for_token(token),
            projects_starred_by_current_user(token),
            token
        )
        count_yours, yours, your_urls = my_mrs_needing_attention(token)
    except requests.exceptions.ConnectionError:
        if i3blocks:
            # https://fontawesome.com/icons/plug?style=solid
            p('{} (no connection for {})'.format('' if awesome else 'disconnected', api_host))
            exit(33)
        else:
            p(no_connection)
        exit(1)

    if i3blocks:
        if os.environ.get('BLOCK_BUTTON') == '1':
            #  xdg-open opens *all* urls that needs attention, on left-click in i3blocks
            for u in other_urls | your_urls:
                subprocess.call(['xdg-open', u])
        if awesome:
            # https://fontawesome.com/icons/code-branch?style=solid
            # https://fontawesome.com/icons/magic?style=solid
            labels = '', ''
        else:
            labels = 'others', 'yours'

        summary="{} {} {} {}".format(labels[0], count_others, labels[1], count_yours)
        p(summary)
        if count_others + count_yours > 0:
            # i3blocks lines logic: https://vivien.github.io/i3blocks/#_format
            p(summary)
            p('#00ff00')
            pass
    else:
        p(ansi_cyan('NEEDS REVIEW ({})'.format(count_others)))
        p(others)
        p(ansi_cyan('YOUR MRS ({})'.format(count_yours)))
        p(yours)
