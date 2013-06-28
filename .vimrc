runtime bundle/vim-pathogen/autoload/pathogen.vim
call pathogen#infect()

filetype plugin indent on
syntax on

" show matching variables on hover
":autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

" spellcheck commit messages
" zg => mark word as good
" z= => get suggestions for improvements
au BufNewFile,BufRead *.markdown,*.md,*.dox,COMMIT_EDITMSG,README,CHANGELOG,INSTALL setlocal spell

color desert
set autoindent
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
set colorcolumn=+1 " highlights the column after textwidth (80, usually)
set tabstop=8
set shiftwidth=8
set noexpandtab

set scrolloff=6
set smartindent
set undolevels=1000
set wildmenu
set ttyfast
set virtualedit+=block
set wildignore+=*/tmp/*,*.so,*.swp,*.zip,*.pyc,*.o
let g:netrw_list_hide= '.*\.so$,.*\.swp$,.*\.zip$,.*\.pyc$,.*\.o$'

" go up one visual line, not logic line
nnoremap j gj
nnoremap k gk

" stay in visual mode after indentation
vnoremap < <gv
vnoremap > >gv

set foldmethod=indent
au FileType gitcommit set nofoldenable
au FileType sass set tabstop=8 shiftwidth=8 noexpandtab
au FileType markdown set textwidth=80

let mapleader=","

" gundo.vim
nnoremap <leader>u :GundoToggle<CR>

" ctags
"set tags=~/.tags
"let Tlist_Ctags_Cmd='/usr/local/bin/ctags'
nnoremap <leader>t :TlistToggle<CR>

" marks
nmap ö `
vmap ö `

" search options
set gdefault
set hlsearch
set ignorecase
set smartcase
set incsearch
set showmatch
set statusline=%<%{getcwd()}/%f\    " Filename
set statusline+=\ %{fugitive#statusline()} "  Git historyotness
set statusline+=\ %=%-14.(%l,%c%V%)\ %p%%  " Right aligned file nav info
"set list
"set listchars=nbsp:¬,tab:>-,extends:»,precedes:«,trail:•

let mapleader=","
nmap <leader>n :nohl<CR>

" buf explorer
nmap <leader><leader> :BufExplorer<CR>

" save file accidentally opened without sudo
" from http://nvie.com/posts/how-i-boosted-my-vim/
cmap w!! w !sudo tee % >/dev/null<CR>

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
inoremap <leader>vdd var_dump(__METHOD__." ".__FILE__.":".__LINE__, );<esc>hOecho "<pre>";<esc>jodie;<esc>kf)i

