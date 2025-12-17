" modeline is nice for markdown files: whitespace/tabs etc.
set modeline
set modelines=5
set incsearch
set ignorecase "needed before smartcase, it seems like
set smartcase
set nonumber
set cursorline
highlight CursorLine cterm=underline ctermbg=NONE guibg=NONE gui=underline

let mapleader = ","

" https://www.reddit.com/r/vim/comments/2k4cbr/problem_with_gj_and_gk/
nnoremap <expr> k (v:count == 0 ? 'gk' : 'k')
nnoremap <expr> j (v:count == 0 ? 'gj' : 'j')

vnoremap > >gv
vnoremap < <gv

" markdown folds on headers
function! MarkdownLevel()
    if getline(v:lnum) =~ '^# .*$'
        return ">1"
    endif
    if getline(v:lnum) =~ '^## .*$'
        return ">2"
    endif
    if getline(v:lnum) =~ '^### .*$'
        return ">3"
    endif
    if getline(v:lnum) =~ '^#### .*$'
        return ">4"
    endif
    if getline(v:lnum) =~ '^##### .*$'
        return ">5"
    endif
    if getline(v:lnum) =~ '^###### .*$'
        return ">6"
    endif
    return "="
endfunction
au BufEnter *.md setlocal foldexpr=MarkdownLevel()
au BufEnter *.md setlocal foldmethod=expr
set foldlevel=20 "default to open (hopefully) all folds


" pretty visualization spaces and tabs (amongst others)
" enable with :set list, disable with :set nolist
set listchars=tab:⇤–⇥,space:·,trail:·,precedes:⇠,extends:⇢,nbsp:×

" ============================================================================
" Transient menu (like emacs magit)
" ============================================================================

" ======================= MENU DEFINITION (edit here) ========================
" Structure: list of groups, each group is a dict of {key: {label, ...options}}
" Options:
"   - 'toggle':   vim option name - shows [ON]/[OFF] and auto-toggles on select
"   - 'items':    nested menu groups (submenu, arbitrarily deep)
"   - 'on':       custom callback (or s:Defer('cmd') for post-close execution)
"   - 'mnemonic': override highlight position (default: finds key in label)
" Auto-added: rulers between groups, quit, back (in submenus)

function! s:Toggle(option)
    execute 'set ' . a:option . '!'
endfunction

function! s:Defer(cmd)
    return {'cmd': a:cmd, 'defer': 1}
endfunction

let s:menu = [
    \ {
    \   'n': {'label': 'numbers',    'toggle': 'number'},
    \   'l': {'label': 'list chars', 'toggle': 'list'},
    \   'w': {'label': 'wrap',       'toggle': 'wrap'},
    \   's': {'label': 'spell',      'toggle': 'spell'},
    \ },
    \ {
    \   'v': {'label': 'vim', 'items': [
    \       {
    \         'v': {'label': 'reload vimrc', 'mnemonic': 8,
    \               'on': s:Defer('source $MYVIMRC | echo "Reloaded vimrc"')},
    \       },
    \   ]},
    \ },
\ ]
" ============================================================================

" State
let s:transient_winid = 0
let s:shadow_winid = 0
let s:menu_path = []  " Stack of keys to reach current menu (empty = root)

" Colors
highlight TransientMenu ctermbg=236 ctermfg=252 guibg=#303030 guifg=#d0d0d0
highlight TransientMenuBorder ctermbg=236 ctermfg=245 guibg=#303030 guifg=#8a8a8a
highlight TransientShadow ctermbg=NONE ctermfg=240 guibg=NONE guifg=#585858
highlight TransientOn ctermbg=236 ctermfg=108 guibg=#303030 guifg=#87af87
highlight TransientOff ctermbg=236 ctermfg=131 guibg=#303030 guifg=#af5f5f
highlight TransientMnemonic ctermbg=236 ctermfg=216 guibg=#303030 guifg=#ffaf87

" Text properties
if empty(prop_type_get('transient_on'))
    call prop_type_add('transient_on', {'highlight': 'TransientOn'})
endif
if empty(prop_type_get('transient_off'))
    call prop_type_add('transient_off', {'highlight': 'TransientOff'})
endif
if empty(prop_type_get('transient_mnemonic'))
    call prop_type_add('transient_mnemonic', {'highlight': 'TransientMnemonic'})
endif

" Get current menu based on path
function! s:GetCurrentMenu()
    let l:menu = s:menu
    for key in s:menu_path
        " Find the item with this key and get its 'items'
        for group in l:menu
            if has_key(group, key)
                let l:menu = group[key].items
                break
            endif
        endfor
    endfor
    return l:menu
endfunction

" Find mnemonic position (1-indexed) in label
function! s:FindMnemonic(label, key)
    let l:pos = stridx(tolower(a:label), tolower(a:key))
    return l:pos >= 0 ? l:pos + 1 : 1
endfunction

" Build a single menu line from key and item definition
function! s:BuildLine(key, item, width)
    " Special cases
    if a:key == '_ruler'
        return {'text': repeat('─', a:width)}
    endif
    if a:key == '_back'
        return {'text': ' ← back'}
    endif
    if a:key == '_quit'
        return {'text': ' quit', 'props': [
            \ {'col': 2, 'length': 1, 'type': 'transient_mnemonic'}
            \ ]}
    endif

    let l:label = a:item.label
    let l:mnemonic_pos = get(a:item, 'mnemonic', s:FindMnemonic(l:label, a:key))

    " Submenu (has 'items')
    if has_key(a:item, 'items')
        let l:text = ' ' . l:label . ' →'
        return {'text': l:text, 'props': [
            \ {'col': l:mnemonic_pos + 1, 'length': 1, 'type': 'transient_mnemonic'}
            \ ]}
    endif

    " Toggle (has 'toggle' key with vim option name)
    if has_key(a:item, 'toggle')
        let l:is_on = eval('&' . a:item.toggle)
        let l:state = l:is_on ? 'ON ' : 'OFF'
        let l:prop_type = l:is_on ? 'transient_on' : 'transient_off'
        let l:text = ' ' . l:label . ' [' . l:state . ']'
        let l:state_col = len(l:text) - 3
        return {'text': l:text, 'props': [
            \ {'col': l:mnemonic_pos + 1, 'length': 1, 'type': 'transient_mnemonic'},
            \ {'col': l:state_col, 'length': 3, 'type': l:prop_type}
            \ ]}
    endif

    " Regular action
    let l:text = ' ' . l:label
    return {'text': l:text, 'props': [
        \ {'col': l:mnemonic_pos + 1, 'length': 1, 'type': 'transient_mnemonic'}
        \ ]}
endfunction

" Build menu content from definition
function! s:GetMenuContent()
    let l:groups = s:GetCurrentMenu()
    let l:in_submenu = !empty(s:menu_path)

    " Collect all items for width calculation
    let l:all_items = []
    for group in l:groups
        for [key, item] in items(group)
            call add(l:all_items, [key, item])
        endfor
    endfor

    " Calculate max width
    let l:max_width = 0
    for [key, item] in l:all_items
        let l:line = s:BuildLine(key, item, 0)
        let l:max_width = max([l:max_width, strwidth(l:line.text)])
    endfor
    " Account for back and quit
    let l:max_width = max([l:max_width, 8])

    " Build lines
    let l:lines = []
    let l:ruler = s:BuildLine('_ruler', {}, l:max_width)
    call add(l:lines, l:ruler)

    for group in l:groups
        " Sort keys for consistent ordering
        let l:keys = sort(keys(group))
        for key in l:keys
            call add(l:lines, s:BuildLine(key, group[key], l:max_width))
        endfor
        call add(l:lines, l:ruler)
    endfor

    " Add back if in submenu
    if l:in_submenu
        call add(l:lines, s:BuildLine('_back', {}, l:max_width))
    endif

    " Add quit
    call add(l:lines, s:BuildLine('_quit', {}, l:max_width))
    return l:lines
endfunction

function! s:GetShadowContent(width, height)
    let l:shadow = []
    let l:dot_line = repeat('⣿', a:width)
    for i in range(a:height)
        call add(l:shadow, l:dot_line)
    endfor
    return l:shadow
endfunction

function! s:CloseMenu(winid)
    call popup_close(a:winid)
    if s:shadow_winid > 0
        call popup_close(s:shadow_winid)
        let s:shadow_winid = 0
    endif
    let s:transient_winid = 0
    let s:menu_path = []
endfunction

function! s:RedrawShadow(content)
    if s:shadow_winid > 0
        call popup_close(s:shadow_winid)
    endif
    let l:width = max(map(copy(a:content), 'strwidth(v:val.text)')) + 4
    let l:height = len(a:content) + 2
    let l:shadow_offset = 2
    let s:shadow_winid = popup_create(s:GetShadowContent(l:width + 1, l:height), #{
        \ line: (&lines / 2) - (l:height / 2) + l:shadow_offset,
        \ col: (&columns / 2) - (l:width / 2) + l:shadow_offset,
        \ zindex: 49,
        \ highlight: 'TransientShadow',
        \ })
endfunction

function! s:RefreshMenu(winid)
    let l:content = s:GetMenuContent()
    call popup_settext(a:winid, l:content)
    call s:RedrawShadow(l:content)
endfunction

" Find item by key in current menu
function! s:FindItem(key)
    let l:groups = s:GetCurrentMenu()
    for group in l:groups
        if has_key(group, a:key)
            return group[a:key]
        endif
    endfor
    return {}
endfunction

function! s:TransientFilter(winid, key)
    " Handle back navigation
    if (a:key == "\<BS>" || a:key == "\<Left>") && !empty(s:menu_path)
        call remove(s:menu_path, -1)
        call s:RefreshMenu(a:winid)
        return 1
    endif

    " Handle quit
    if a:key == 'q'
        call s:CloseMenu(a:winid)
        return 1
    endif

    let l:item = s:FindItem(a:key)
    if !empty(l:item)
        " Submenu navigation
        if has_key(l:item, 'items')
            call add(s:menu_path, a:key)
            call s:RefreshMenu(a:winid)
            return 1
        endif

        " Toggle (derived action)
        if has_key(l:item, 'toggle')
            call s:Toggle(l:item.toggle)
        " Explicit action callback
        elseif has_key(l:item, 'on')
            let l:on = l:item.on
            " Deferred execution (dict with 'cmd' and 'defer')
            if type(l:on) == v:t_dict && get(l:on, 'defer', 0)
                let l:cmd = l:on.cmd
                call s:CloseMenu(a:winid)
                call timer_start(0, {-> execute(l:cmd)})
                return 1
            endif
            " Direct callback (funcref/lambda)
            if type(l:on) == v:t_func
                call l:on()
            endif
        endif
    endif

    call s:CloseMenu(a:winid)
    return 1
endfunction

function! s:OpenTransientMenu()
    if s:transient_winid > 0 && popup_getpos(s:transient_winid) != {}
        call s:CloseMenu(s:transient_winid)
        return
    endif

    let l:content = s:GetMenuContent()
    let l:width = max(map(copy(l:content), 'strwidth(v:val.text)')) + 4
    let l:height = len(l:content) + 2

    let l:shadow_offset = 2
    let s:shadow_winid = popup_create(s:GetShadowContent(l:width + 1, l:height), #{
        \ line: (&lines / 2) - (l:height / 2) + l:shadow_offset,
        \ col: (&columns / 2) - (l:width / 2) + l:shadow_offset,
        \ zindex: 49,
        \ highlight: 'TransientShadow',
        \ })

    let s:transient_winid = popup_create(l:content, #{
        \ title: ' Menu ',
        \ border: [],
        \ borderchars: ['─', '│', '─', '│', '┌', '┐', '┘', '└'],
        \ padding: [0, 1, 0, 1],
        \ pos: 'center',
        \ zindex: 50,
        \ filter: function('s:TransientFilter'),
        \ mapping: 0,
        \ highlight: 'TransientMenu',
        \ borderhighlight: ['TransientMenuBorder'],
        \ })
endfunction

nnoremap <leader><leader> :call <SID>OpenTransientMenu()<CR>
