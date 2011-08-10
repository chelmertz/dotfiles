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

" syntastic settigns
let g:syntastic_auto_loc_list=1
set statusline+=%#warningmsg#
set statusline+=%{SyntasticStatuslineFlag()}
set statusline+=%*
nmap <Leader>e :Errors<CR>


let mapleader=","
" open a new terminal split, from vim-conque plugin
nmap <Leader>bs :ConqueTermVSplit bash<CR>
nmap <Leader>bv :ConqueTermTab bash<CR>
