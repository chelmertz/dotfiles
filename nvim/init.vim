" binding strategy:
" leader+d => lookup *d*efinition for word under cursor
" leader+m => *m*ake current file

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
Plug 'luochen1990/rainbow' " rainbow parens, mainly for clojure
Plug 'bling/vim-airline' " neovim does not have a default for the status bar, use the most popular option
Plug 'ElmCast/elm-vim' " requires npm install {elm,elm-test,elm-oracle,elm-format}
Plug 'vim-syntastic/syntastic' " error checking
Plug 'fatih/vim-go' " go syntax and lots of functionality

" python
Plug 'zchee/deoplete-jedi'
Plug 'hdima/python-syntax'

" js
Plug 'carlitux/deoplete-ternjs', { 'do': 'npm install -g tern' }

" c, c++
Plug 'zchee/deoplete-clang'

" brought over from plain-old-vim-workflow
Plug 'tpope/vim-surround' " exchange delimiters for others, use delimiters for text objects
Plug 'tpope/vim-fugitive' " git support, see :Gblame in a tracked file
Plug 'thinca/vim-visualstar' " make * search for visual selection rather than finding a text object before
Plug 'haya14busa/incsearch.vim' " highlight all matched patterns while writing search keyword
Plug 'guns/vim-clojure-static' " highlighting, indentation etc for clojure
Plug 'tpope/vim-fireplace' " clojure repl
Plug 'morhetz/gruvbox' " pretty colors, dark scheme
Plug 'vim-scripts/matchit.zip' " % to not only jump to matching brace, but to if-endif, switch-cases etc
Plug 'scrooloose/nerdcommenter' " leader+c+c to toggle comment of visual selection
Plug 'ap/vim-css-color' " display actual color of rgb + hex notation in css files
Plug 'ctrlpvim/ctrlp.vim' " ctrlp to search through files

call plug#end()

filetype plugin indent on
syntax enable

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
au FileType elm nmap <leader>d <Plug>(elm-show-docs)
au FileType elm nmap <leader>m <Plug>(elm-make)

" python
let g:deoplete#sources#jedi#show_docstring = 1

" c/c++
let g:deoplete#sources#clang#libclang_path = '/usr/lib64/libclang.so'
let g:deoplete#sources#clang#clang_header = '/usr/lib64/clang'

" elm
autocmd Filetype elm setlocal ts=4 sw=4 expandtab
"autocmd BufWritePost *.elm ElmMakeCurrentFile "depends on lambdatoast/elm.vim
let g:elm_format_autosave = 1

let mapleader = ","

colorscheme gruvbox
set bg=dark

set gdefault
set ignorecase
set smartcase
set foldmethod=indent

" Selecting text in neovim in a terminal enters visual mode and disables the
" select + middle-click worfklow. Press shift while selecting text with the
" mouse to avoid this.... or just disable it
set mouse= " disable neovim's annoying 'enter visual mode on mouse selection'; I just want to place it in a buffer, dammit!

let g:rainbow_active = 1

let g:airline_powerline_fonts = 1 " available after dnf install powerline-fonts
