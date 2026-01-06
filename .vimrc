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

" nix config
autocmd FileType nix setlocal shiftwidth=2 softtabstop=2 expandtab

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
" Structure: list of columns, each column is a dict with '_header' and {key: item}
" Item options:
"   - 'toggle':   vim option name - shows [ON]/[OFF] and auto-toggles on select
"   - 'on':       custom callback (or s:Defer('cmd') for post-close execution)

function! s:Toggle(option)
    execute 'set ' . a:option . '!'
endfunction

function! s:Defer(cmd)
    return {'cmd': a:cmd, 'defer': 1}
endfunction

" File finder using fd + fzf (no plugins)
let s:find_file_temp = ''

function! s:FindFile()
    let s:find_file_temp = tempname()
    let l:cmd = 'fd --type f 2>/dev/null | fzf > ' . shellescape(s:find_file_temp)

    call term_start(['bash', '-c', l:cmd], #{
        \ term_rows: 15,
        \ term_finish: 'close',
        \ exit_cb: function('s:FindFileCallback'),
        \ })
endfunction

function! s:FindFileCallback(job, status)
    " Small delay to let terminal close cleanly
    call timer_start(10, function('s:FindFileOpen'))
endfunction

function! s:FindFileOpen(timer)
    if filereadable(s:find_file_temp)
        let l:lines = readfile(s:find_file_temp)
        call delete(s:find_file_temp)
        if len(l:lines) > 0 && l:lines[0] != ''
            execute 'edit ' . fnameescape(l:lines[0])
        endif
    endif
endfunction

let s:menu = [
    \ {'_header': 'Toggles',
    \   'n': {'label': 'line numbers',  'toggle': 'number'},
    \   'c': {'label': 'chars display', 'toggle': 'list'},
    \   'w': {'label': 'wrap',          'toggle': 'wrap'},
    \   's': {'label': 'spell',         'toggle': 'spell'},
    \ },
    \ {'_header': 'Files',
    \   'f': {'label': 'find file', 'on': s:Defer('call s:FindFile()')},
    \ },
    \ {'_header': 'Vim',
    \   'v': {'label': 'reload vimrc',
    \         'on': s:Defer('source $MYVIMRC | echo "Reloaded vimrc"')},
    \ },
\ ]

" Build mdlink column from D_MDLINK_DATA environment variable
" Expected format: {"header":"Tags","items":[{"path":"work:matchi:uriel","name":"Uriel"}]}
" Uses 't' as prefix key, assigns keys based on starting letter of name
function! s:BuildMdlinkColumn()
    if !exists('$D_MDLINK_DATA') || $D_MDLINK_DATA == ''
        return {}
    endif

    try
        let l:data = json_decode($D_MDLINK_DATA)
    catch
        return {}
    endtry

    let l:col = {'_header': get(l:data, 'header', 'Tags'), '_prefix': 't'}
    let l:used_keys = {}
    let l:fallback_keys = 'abcdefghijklmnopqrstuvwxyz0123456789'

    for item in get(l:data, 'items', [])
        let l:label = get(item, 'name', item.path)
        let l:key = ''

        " Try to find a key from the label characters
        for i in range(len(l:label))
            let l:char = tolower(l:label[i])
            if l:char =~ '[a-z0-9]' && !has_key(l:used_keys, l:char)
                let l:key = l:char
                break
            endif
        endfor

        " Fallback: find any unused key
        if l:key == ''
            for i in range(len(l:fallback_keys))
                let l:char = l:fallback_keys[i]
                if !has_key(l:used_keys, l:char)
                    let l:key = l:char
                    break
                endif
            endfor
        endif

        if l:key != ''
            let l:used_keys[l:key] = 1
            let l:col[l:key] = {
                \ 'label': l:label,
                \ 'mdlink': item.path,
            \ }
        endif
    endfor

    return l:col
endfunction

" Store mdlink column separately for prefix key lookup
let s:mdlink_col = {}

" Get dynamic menu (base menu + mdlink column if available)
function! s:GetDynamicMenu()
    let l:menu = deepcopy(s:menu)

    let s:mdlink_col = s:BuildMdlinkColumn()
    if !empty(s:mdlink_col)
        call add(l:menu, s:mdlink_col)
    endif

    return l:menu
endfunction
" ============================================================================

" State
let s:transient_winid = 0

" Colors
highlight TransientMenu ctermbg=236 ctermfg=252 guibg=#303030 guifg=#d0d0d0
highlight TransientMenuBorder ctermbg=236 ctermfg=245 guibg=#303030 guifg=#8a8a8a
highlight TransientOn ctermbg=236 ctermfg=108 guibg=#303030 guifg=#87af87
highlight TransientOff ctermbg=236 ctermfg=131 guibg=#303030 guifg=#af5f5f
highlight TransientMnemonic ctermbg=236 ctermfg=216 guibg=#303030 guifg=#ffaf87
highlight TransientHeader ctermbg=236 ctermfg=252 cterm=bold guibg=#303030 guifg=#d0d0d0 gui=bold

" Completion popup (works in both dark and light terminals)
highlight Pmenu ctermbg=237 ctermfg=252 guibg=#3a3a3a guifg=#d0d0d0
highlight PmenuSel ctermbg=241 ctermfg=231 cterm=bold guibg=#626262 guifg=#ffffff gui=bold
highlight PmenuSbar ctermbg=238 guibg=#444444
highlight PmenuThumb ctermbg=248 guibg=#a8a8a8

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
if empty(prop_type_get('transient_header'))
    call prop_type_add('transient_header', {'highlight': 'TransientHeader'})
endif

" Build column content from a column definition
function! s:BuildColumn(col_def)
    let l:lines = []
    let l:header = get(a:col_def, '_header', '')
    let l:prefix_key = get(a:col_def, '_prefix', '')

    " Header line (bold)
    if l:header != ''
        let l:header_text = l:header
        if l:prefix_key != ''
            let l:header_text .= ' [' . l:prefix_key . ' ...]'
        endif
        call add(l:lines, {'text': l:header_text, 'props': [
            \ {'col': 1, 'length': len(l:header), 'type': 'transient_header'}
            \ ]})
    endif

    " Calculate max label width for alignment
    let l:keys = sort(filter(keys(a:col_def), 'v:val[0] != "_"'))
    let l:max_label_width = 0
    for key in l:keys
        let l:max_label_width = max([l:max_label_width, strwidth(a:col_def[key].label)])
    endfor

    " Items (sorted by key)
    for key in l:keys
        let l:item = a:col_def[key]
        let l:label = l:item.label
        let l:display_key = l:prefix_key != '' ? l:prefix_key . ' ' . key : key

        if has_key(l:item, 'toggle')
            let l:is_on = eval('&' . l:item.toggle)
            let l:state = l:is_on ? 'ON' : 'OFF'
            let l:prefix = l:is_on ? '   ' : '  '
            let l:prop_type = l:is_on ? 'transient_on' : 'transient_off'
            let l:padded_label = l:label . repeat(' ', l:max_label_width - strwidth(l:label))
            let l:text = l:display_key . ' ' . l:padded_label . l:prefix . '[' . l:state . ']'
            let l:state_col = len(l:text) - len(l:state)
            let l:props = [
                \ {'col': 1, 'length': len(l:display_key), 'type': 'transient_mnemonic'},
                \ {'col': l:state_col, 'length': len(l:state), 'type': l:prop_type}
                \ ]
            call add(l:lines, {'text': l:text, 'props': l:props})
        else
            let l:text = l:display_key . ' ' . l:label
            let l:props = [
                \ {'col': 1, 'length': len(l:display_key), 'type': 'transient_mnemonic'}
                \ ]
            call add(l:lines, {'text': l:text, 'props': l:props})
        endif
    endfor

    return l:lines
endfunction

" Get max height across all columns
function! s:GetMaxColumnHeight(menu)
    let l:max = 0
    for col in a:menu
        let l:height = len(filter(keys(deepcopy(col)), 'v:val[0] != "_"'))
        if has_key(col, '_header')
            let l:height += 1
        endif
        let l:max = max([l:max, l:height])
    endfor
    return l:max
endfunction

" Get column width
function! s:GetColumnWidth(col_def)
    let l:max = 0
    let l:prefix_key = get(a:col_def, '_prefix', '')
    if has_key(a:col_def, '_header')
        let l:header_text = a:col_def._header
        if l:prefix_key != ''
            let l:header_text .= ' [' . l:prefix_key . ' ...]'
        endif
        let l:max = len(l:header_text)
    endif
    for [key, item] in items(a:col_def)
        if key[0] != '_'
            let l:display_key = l:prefix_key != '' ? l:prefix_key . ' ' . key : key
            let l:text = l:display_key . ' ' . item.label
            if has_key(item, 'toggle')
                let l:text .= ' [OFF]'
            endif
            let l:max = max([l:max, len(l:text)])
        endif
    endfor
    return l:max
endfunction

" Build menu content with columns side by side
function! s:GetMenuContent()
    let l:menu = s:GetDynamicMenu()
    let l:num_cols = len(l:menu)
    let l:col_gap = 3
    let l:total_width = &columns - 4  " Account for border/padding

    " Calculate column widths
    let l:col_widths = []
    for col in l:menu
        call add(l:col_widths, s:GetColumnWidth(col))
    endfor

    " Build each column's content
    let l:columns = []
    for col in l:menu
        call add(l:columns, s:BuildColumn(col))
    endfor

    " Get max height
    let l:max_height = s:GetMaxColumnHeight(l:menu)

    " Merge columns into rows
    let l:lines = []
    for row in range(l:max_height)
        let l:row_text = ''
        let l:row_props = []
        let l:col_offset = 0

        for col_idx in range(l:num_cols)
            let l:col_content = l:columns[col_idx]
            let l:col_width = l:col_widths[col_idx]

            if row < len(l:col_content)
                let l:cell = l:col_content[row]
                let l:cell_text = l:cell.text

                " Pad to column width
                let l:padded = l:cell_text . repeat(' ', l:col_width - strwidth(l:cell_text))
                let l:row_text .= l:padded

                " Adjust prop positions for column offset
                for prop in get(l:cell, 'props', [])
                    call add(l:row_props, {
                        \ 'col': prop.col + l:col_offset,
                        \ 'length': prop.length,
                        \ 'type': prop.type
                        \ })
                endfor
            else
                " Empty cell
                let l:row_text .= repeat(' ', l:col_width)
            endif

            " Update offset for next column (use strwidth of actual text added)
            let l:col_offset = strwidth(l:row_text)
            if col_idx < l:num_cols - 1
                let l:row_text .= repeat(' ', l:col_gap)
                let l:col_offset = strwidth(l:row_text)
            endif
        endfor

        call add(l:lines, {'text': l:row_text, 'props': l:row_props})
    endfor

    " Add quit line
    call add(l:lines, {'text': ''})
    call add(l:lines, {'text': 'q quit', 'props': [
        \ {'col': 1, 'length': 1, 'type': 'transient_mnemonic'}
        \ ]})

    return l:lines
endfunction

function! s:CloseMenu(winid)
    call popup_close(a:winid)
    let s:transient_winid = 0
endfunction

" Find item by key across all columns (skip columns with prefix keys)
function! s:FindItem(key)
    let l:menu = s:GetDynamicMenu()
    for col in l:menu
        " Skip columns that require a prefix key
        if has_key(col, '_prefix')
            continue
        endif
        if has_key(col, a:key)
            return col[a:key]
        endif
    endfor
    return {}
endfunction

function! s:TransientFilter(winid, key)
    " Handle quit/escape
    if a:key == 'q' || a:key == "\<Esc>"
        call s:CloseMenu(a:winid)
        return 1
    endif

    " Handle 't' prefix for mdlink items
    if a:key == 't' && !empty(s:mdlink_col)
        " Wait for next key
        redraw
        echo 't-'
        let l:char = getchar()
        echo ''
        " Escape cancels the prefix, keeps menu open
        if l:char == 27 || l:char == "\<Esc>"
            return 1
        endif
        let l:next_key = nr2char(l:char)
        if has_key(s:mdlink_col, l:next_key)
            let l:item = s:mdlink_col[l:next_key]
            if has_key(l:item, 'mdlink')
                let l:tag = l:item.mdlink
                let l:label = l:item.label
                let l:link = '[' . l:label . '](d:tag:' . l:tag . ')'
                call s:CloseMenu(a:winid)
                call timer_start(0, {-> s:InsertText(l:link)})
                return 1
            endif
        endif
        " Invalid second key, just close
        call s:CloseMenu(a:winid)
        return 1
    endif

    let l:item = s:FindItem(a:key)
    if !empty(l:item)
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

" Insert text at cursor position (used by mdlink)
function! s:InsertText(text)
    execute "normal! a" . a:text . "\<Esc>"
endfunction

function! s:OpenTransientMenu()
    if s:transient_winid > 0 && popup_getpos(s:transient_winid) != {}
        call s:CloseMenu(s:transient_winid)
        return
    endif

    let l:content = s:GetMenuContent()
    let l:height = len(l:content)

    let s:transient_winid = popup_create(l:content, #{
        \ line: &lines - l:height + 8,
        \ col: 1,
        \ minwidth: &columns - 2,
        \ maxwidth: &columns - 2,
        \ border: [1, 0, 1, 1],
        \ borderchars: ['═', '║', '═', '║', '╔', '╗', '╝', '╚'],
        \ padding: [0, 1, 0, 1],
        \ zindex: 50,
        \ filter: function('s:TransientFilter'),
        \ mapping: 0,
        \ highlight: 'TransientMenu',
        \ borderhighlight: ['TransientMenuBorder'],
        \ })
endfunction

nnoremap <leader><leader> :call <SID>OpenTransientMenu()<CR>
