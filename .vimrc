" pathogen - uses .vim/bundle/<plugin> to load vim plugins
source ~/.vim/bundle/vim-pathogen/autoload/pathogen.vim
call pathogen#infect()
syntax on

filetype plugin indent on

" show matching variables on hover
:autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

" spellcheck commit messages
" zg => mark word as good
" z= => get suggestions for improvements
au BufNewFile,BufRead COMMIT_EDITMSG setlocal spell

set autoindent
set background=dark
colorscheme Tomorrow-Night-Eighties
set copyindent
set cursorline
set backspace=indent,eol,start
set hidden
set history=1000
set laststatus=2
set nobackup
set modelines=0
set nocompatible
set noerrorbells
set nostartofline
set nonumber
set showcmd

set scrolloff=6
set smartindent
set undolevels=1000
set wildmenu
set ttyfast
set virtualedit+=block

" go up one visual line, not logic line
nnoremap j gj
nnoremap k gk

" stay in visual mode after indentation
vnoremap < <gv
vnoremap > >gv

let mapleader=","
" open a new terminal split, from vim-conque plugin
nmap <leader>bash :ConqueTerm bash<CR>

" gundo.vim
nnoremap <leader>u :GundoToggle<CR>

" ctags
set tags=~/.tags
let Tlist_Ctags_Cmd='/usr/local/bin/ctags'
nnoremap <leader>t :TlistToggle<CR>

" marks
nmap ö `
vmap ö `

" search options
set gdefault
set hlsearch
set ignorecase
set incsearch
set showmatch
set statusline=%<%{getcwd()}/%f\    " Filename
set statusline+=\ %{fugitive#statusline()} "  Git historyotness
set statusline+=\ %=%-14.(%l,%c%V%)\ %p%%  " Right aligned file nav info
nnoremap / /\v
vnoremap / /\v

let mapleader=","
nmap <leader>n :nohl<CR>

" search recursively from current folder with Ack
nmap <leader>s :Ack 

" buf explorer
nmap <leader><leader> :BufExplorer<CR>

" syntastic settigns
let g:syntastic_auto_loc_list=1
let g:syntastic_auto_jump=1
set statusline+=%#warningmsg#
set statusline+=%{SyntasticStatuslineFlag()}
set statusline+=%*
nmap <leader>e :Errors<CR>

" save file accidentally opened without sudo
" from http://nvie.com/posts/how-i-boosted-my-vim/
cmap w!! w !sudo tee % >/dev/null

" git
nmap <leader>gs :Extradite<CR> " gs for 'git show'

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
