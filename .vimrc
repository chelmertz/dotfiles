" pathogen - uses .vim/bundle/<plugin> to load vim plugins
call pathogen#runtime_append_all_bundles()

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
