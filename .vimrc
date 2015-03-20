runtime bundle/vim-pathogen/autoload/pathogen.vim
call pathogen#infect()

filetype plugin indent on
syntax on

" to paste from the 'selection register':
" <C-r> + +
" .... since shift + insert does not work in gvim (but it does in terminal..)

" show matching variables on hover
":autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

" general tip: when keeping a diary-like log file of "first I did this, then
" that", do :r !date to get a well formed timestamp before you start

" spellcheck commit messages
" zg => mark word as good
" z= => get suggestions for improvements
au BufNewFile,BufRead *.markdown,*.md,*.dox,COMMIT_EDITMSG,README,CHANGELOG,INSTALL setlocal spell

" highlight trailing whitespace,
" http://vim.wikia.com/wiki/Highlight_unwanted_spaces
au ColorScheme * highlight ExtraWhitespace ctermbg=red guibg=red
"match ExtraWhitespace /\s\+$\| \+\ze\t/
au InsertEnter * match ExtraWhitespace /\s\+\%#\@<!$/
au BufEnter,InsertLeave * match ExtraWhitespace /\s\+$/

set guioptions=agitc
set guifont=Monospace\ 11

color badwolf " desert molokai
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
set undolevels=1000
set wildmenu
set ttyfast
set virtualedit+=block
set wildignore+=*/tmp/*,*.so,*.swp,*.zip,*.pyc,*.o

" use gx on a URL
let g:netrw_browsex_viewer="firefox"
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
" install Exuberant ctags
" cd ~; ctags code
" # open a file
" # ctrl+] when cursor on variable
" # ctrl+t to return
" ,t to open overview of file
" more: http://vim.wikia.com/wiki/Browsing_programs_with_tags
set tags=tags,./tags,~/tags;/
"nnoremap <leader>c <C
"let Tlist_Ctags_Cmd='ctags'
"nnoremap <leader>t :TlistToggle<CR>
nnoremap <leader>t :TagbarToggle<CR>

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

" incsearch: show all matches right away
map / <Plug>(incsearch-forward)
map ? <Plug>(incsearch-backward)

" make shift + insert paste from selection buffer even in gvim
if has("gui_running")
	imap <silent> <S-Insert> <Esc>"+pa
endif

" vimwiki
" auto_export generates html each time a buffer is saved
let g:vimwiki_list = [{
	\ 'path': '~/Dropbox/vimwiki',
	\ 'template_path': '~/Dropbox/vimwiki/style/',
	\ 'template_default': 'template',
	\ 'template_ext': '.html',
	\ 'css_name': '~/Dropbox/vimwiki/style/style.css',
	\ 'auto_export': 1
	\ }]

" buf explorer
" after finding the buffer you want to switch to, :bN switches to buffer N
" or just <leader>b to go to next buffer
nnoremap <leader><leader> :ls<CR>

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
inoremap <leader>vdd var_dump(__METHOD__." ".__FILE__.":".__LINE__, );<esc>odie(1);<esc>kf)i

inoremap <leader>cl console.log();<esc>F)i


""" debugging tips:
""" http://vim.wikia.com
""" :command (list user defined commands)
""" :scriptnames (list loaded plugins)
""" vim -u NONE (run vim without plugins or vimrc)
""" vim -u NORC (run vim with plugins, without vimrc)
""" :echo g:colors_name <-- current scheme
""" :echo &ft <-- current filetype
