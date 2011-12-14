set guifont=Monaco:h12
set background=dark
"colorscheme Tomorrow-Night-Eighties
colorscheme Monokai

"save and close all files and save global session
nnoremap <leader>q :mksession! ~/.vim/Session.vim<CR>:wqa<CR>

"close all files without saving and save global session
nnoremap <leader>www :mksession! ~/.vim/Session.vim<CR>:qa!<CR>

function! RestoreSession()
  if argc() == 0 "vim called without arguments
    execute 'source ~/.vim/Session.vim'
  end
endfunction
autocmd VimEnter * call RestoreSession()
