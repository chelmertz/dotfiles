filetype off
set rtp+=~/.vim/vundles/vundle
call vundle#rc('~/.config/dotfiles/.vim/vundles')
" on new machines/to update bundles/get the files of newly added ones:
" :BundleInstall
Bundle 'gmarik/vundle'

Bundle 'vim-scripts/matchit.zip'
Bundle 'sickill/vim-monokai'
Bundle 'tpope/vim-fugitive'
Bundle 'scrooloose/nerdcommenter'
Bundle 'ervandew/supertab'
Bundle 'spf13/vim-markdown'
Bundle 'scrooloose/syntastic'
Bundle 'mattn/zencoding-vim'
Bundle 'tpope/vim-surround'
Bundle 'tpope/vim-repeat'
Bundle 'groenewege/vim-less'
Bundle 'wolverian/Tomorrow-Theme'
Bundle 'thisivan/vim-bufexplorer'
Bundle 'kien/ctrlp.vim'
Bundle 'vim-scripts/taglist.vim'
Bundle 'sjl/gundo.vim'
Bundle 'chelmertz/snipmate.vim'
Bundle 'vim-scripts/vimwiki'

filetype plugin indent on
syntax on

" show matching variables on hover
:autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

" spellcheck commit messages
" zg => mark word as good
" z= => get suggestions for improvements
au BufNewFile,BufRead *.markdown,*.md,COMMIT_EDITMSG,README,CHANGELOG,INSTALL setlocal spell

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
set wildignore+=*/tmp/*,*.so,*.swp,*.zip,*.pyc

" go up one visual line, not logic line
nnoremap j gj
nnoremap k gk

" stay in visual mode after indentation
vnoremap < <gv
vnoremap > >gv

set foldmethod=indent

let mapleader=","

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
set smartcase
set incsearch
set showmatch
set statusline=%<%{getcwd()}/%f\    " Filename
set statusline+=\ %{fugitive#statusline()} "  Git historyotness
set statusline+=\ %=%-14.(%l,%c%V%)\ %p%%  " Right aligned file nav info
nnoremap / /\v
vnoremap / /\v
"set list
"set listchars=nbsp:¬,tab:>-,extends:»,precedes:«,trail:•

let mapleader=","
nmap <leader>n :nohl<CR>

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
nmap <leader>f A {<esc>jo}<esc>
