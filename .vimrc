" pathogen - uses .vim/bundle/<plugin> to load vim plugins
source ~/.vim/bundle/vim-pathogen/autoload/pathogen.vim
call pathogen#infect()
syntax on

filetype on
filetype plugin on
filetype indent on


" show matching variables on hover
:autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

set autoindent
set hlsearch
set incsearch
set laststatus=2
set number
set scrolloff=6
set smartindent
set wildmenu


let mapleader=","
" open a new terminal split
nmap <Leader>bs :ConqueTermVSplit bash<CR>
nmap <Leader>bv :ConqueTermTab bash<CR>
