" binding strategy:
" leader+d => lookup *d*efinition for word under cursor
" leader+m => *m*ake current file
" leader+e => show *e*rrors from syntastic's compilation/analysis
" <f4> switch between light/dark theme

if &compatible
  set nocompatible
endif

" Whenever you add a new plugin, start nvim and
" :PlugInstall
" This requires vim-plug (which is 10 times as nice as dein, interface and
" feedback-wise)
call plug#begin('~/.local/share/nvim/plugged')

Plug 'Shougo/deoplete.nvim' " async completion engine
Plug 'neomake/neomake' " async :make
Plug 'bling/vim-airline' " neovim does not have a default for the status bar, use the most popular option
Plug 'vim-syntastic/syntastic' " error checking

" go
Plug 'fatih/vim-go' " go syntax and lots of functionality

" elm
Plug 'ElmCast/elm-vim' " requires npm install {elm,elm-test,elm-oracle,elm-format}

" python
Plug 'zchee/deoplete-jedi'
Plug 'hdima/python-syntax'

" js
Plug 'carlitux/deoplete-ternjs', { 'do': 'npm install -g tern' }

" c, c++
Plug 'zchee/deoplete-clang'

" clojure
Plug 'guns/vim-clojure-static' " highlighting, indentation etc for clojure
Plug 'tpope/vim-fireplace' " clojure repl

" colors
Plug 'morhetz/gruvbox' " pretty colors, dark scheme
Plug 'vim-scripts/louver.vim' " a light colorscheme
Plug 'ap/vim-css-color' " display actual color of rgb + hex notation in css files
Plug 'luochen1990/rainbow' " rainbow parens, mainly for clojure
" nice alternative themes: badwolf, github, molokai, desert

" git
Plug 'tpope/vim-fugitive' " git support, see :Gblame in a tracked file

" vim behavior
Plug 'tpope/vim-surround' " exchange delimiters for others, use delimiters for text objects
Plug 'thinca/vim-visualstar' " make * search for visual selection rather than finding a text object before
Plug 'vim-scripts/matchit.zip' " % to not only jump to matching brace, but to if-endif, switch-cases etc
Plug 'haya14busa/incsearch.vim' " highlight all matched patterns while writing search keyword
Plug 'scrooloose/nerdcommenter' " leader+c+c to toggle comment of visual selection
Plug 'ctrlpvim/ctrlp.vim' " ctrlp to search through files

call plug#end()

filetype plugin indent on
syntax enable
let mapleader = ","

nnoremap <leader>e :Errors<cr>

" spellcheck
" zg => mark word as good
" z= => get suggestions for improvements
autocmd BufNewFile,BufRead *.wiki,*.markdown,*.md,*.dox,COMMIT_EDITMSG,README,CHANGELOG,INSTALL setlocal spell

" autocomplete
inoremap <expr><tab> pumvisible() ? "\<c-n>" : "\<tab>"
let g:deoplete#enable_at_startup = 1
let g:deoplete#omni#functions = {}
let g:deoplete#omni#input_patterns = {}
let g:deoplete#sources = {}

" elm
let g:deoplete#omni#functions.elm = ['elm#Complete']
let g:deoplete#omni#input_patterns.elm = '[^ \t]+'
let g:deoplete#sources#elm = ['omni']
autocmd FileType elm nmap <leader>d <Plug>(elm-show-docs)
autocmd FileType elm nmap <leader>m <Plug>(elm-make)
autocmd Filetype elm setlocal ts=4 sw=4 expandtab
let g:elm_format_autosave = 1

" python
let g:deoplete#sources#jedi#show_docstring = 1

" ruby
autocmd FileType ruby setlocal ts=2 sw=2 expandtab

" c/c++
let g:deoplete#sources#clang#libclang_path = '/usr/lib64/libclang.so'
let g:deoplete#sources#clang#clang_header = '/usr/lib64/clang'

" toggle light/dark color scheme with <F4>
let s:lightscheme = 0
function! ToggleBackgroundLightness()
	if s:lightscheme
		colorscheme gruvbox
		set bg=dark
		let s:lightscheme = 0
		let g:rainbow_active = 1
	else
		colorscheme louver
		set bg=light
		let s:lightscheme = 1
		" Deactivating rainbow colors because it's messy to configure,
		" and configured for dark schemes by default. I.e. Clojure
		" will be coded from within a dark theme
		let g:rainbow_active = 0
	endif
endfunction
nnoremap <F4> :call ToggleBackgroundLightness()<CR>
call ToggleBackgroundLightness()

set foldmethod=indent
autocmd FileType gitcommit set nofoldenable

set gdefault " regexes automatically gets /g
set ignorecase
set smartcase
nmap <leader>n :nohl<CR>
" incsearch: show all matches right away
map / <Plug>(incsearch-forward)
map ? <Plug>(incsearch-backward)


" enter visual mode after < and > to continue indenting
vnoremap < <gv
vnoremap > >gv
" go up one visual line, not logic line
nnoremap j gj
nnoremap k gk

" Selecting text in neovim in a terminal enters visual mode and disables the
" select + middle-click worfklow. Press shift while selecting text with the
" mouse to avoid this.... or just disable it
set mouse= " disable neovim's annoying 'enter visual mode on mouse selection'; I just want to place it in a buffer, dammit!

" restore cursor position next time the file is opened, from
" http://vim.wikia.com/wiki/Restore_cursor_to_file_position_in_previous_editing_session
function! ResCur()
	if line("'\"") <= line("$")
		normal! g`"
		return 1
	endif
endfunction
augroup resCur
	autocmd!
	autocmd BufWinEnter * call ResCur()
augroup END

let g:airline_powerline_fonts = 1 " available after dnf install powerline-fonts (in Fedora at least)

" transform
" if(a)
" 	echo 'yes';
" to
" if(a) {
" 	echo 'yes';
" }
nnoremap <leader>f A {<esc>jo}<esc>

" S requires the vim-surround plugin
vmap <leader>vdd Sbivar_dump<esc>f(a__METHOD__." ".__FILE__.":".__LINE__, <esc>f)a;<esc>
inoremap <leader>vdd if(PHP_SAPI != 'cli' && !headers_sent()) header("HTTP/1.1 500 Internal Server Error");<esc>oecho "<pre>";<esc>ovar_dump(__METHOD__." ".__FILE__.":".__LINE__, );<esc>odie(1);<esc>k0f)i

inoremap <leader>cl console.log();<esc>F)i
