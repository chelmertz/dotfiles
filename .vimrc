" pathogen - uses .vim/bundle/<plugin> to load vim plugins
source ~/.vim/bundle/vim-pathogen/autoload/pathogen.vim
call pathogen#infect()
syntax on

filetype plugin indent on

" show matching variables on hover
:autocmd CursorMoved * silent! exe printf('match IncSearch /\<%s\>/', expand('<cword>'))

set autoindent
set background=dark
colorscheme Tomorrow-Night
set copyindent
set cursorline
set hidden
set history=1000
set hlsearch
set ignorecase
set incsearch
set laststatus=2
set nobackup
set noerrorbells
set nostartofline
set number
set showcmd
set scrolloff=6
set showmatch
set smartindent
set undolevels=1000
set wildmenu

let mapleader=","
" open a new terminal split, from vim-conque plugin
nmap <Leader>bash :ConqueTerm bash<CR>

" search recursively from current folder with Ack
nmap <Leader>s :Ack 

" syntastic settigns
let g:syntastic_auto_loc_list=1
let g:syntastic_auto_jump=1
set statusline+=%#warningmsg#
set statusline+=%{SyntasticStatuslineFlag()}
set statusline+=%*
nmap <Leader>e :Errors<CR>

" vim-task (todo) plugin
noremap <silent> <buffer><leader>w :call Toggle_task_status()<CR>

" save file accidentally opened without sudo
" from http://nvie.com/posts/how-i-boosted-my-vim/
cmap w!! w !sudo tee % >/dev/null

" git
nmap <Leader>gs :Extradite<CR> " gs for 'git show'

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

" Open URL
command -bar -nargs=1 OpenURL :!open <args>
function! OpenURL()
	let s:uri = matchstr(getline("."), '[a-z]*:\/\/[^ >,;:]*')
	echo s:uri
	if s:uri != ""
		exec "!open \"" . s:uri . "\""
	else
		echo "No URI found in line."
	endif
endfunction
map <Leader>u :call OpenURL()<CR>

" navigate buffers
map <C-l> <C-w>l
map <C-h> <C-w>h
