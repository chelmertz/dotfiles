" Selecting text in neovim in a terminal enters visual mode and disables the
" select + middle-click worfklow. Press shift while selecting text with the
" mouse to avoid this.

if &compatible
  set nocompatible
endif

" Use dein.vim's installer with the path ~/.config/nvim
set runtimepath+=~/.config/nvim/repos/github.com/Shougo/dein.vim

" Whenever you add a new plugin, start nvim and
" :call dein#install()
if dein#load_state('~/.config/nvim')
  call dein#begin('~/.config/nvim')

  " new for neovim
  call dein#add('~/.config/nvim/repos/github.com/Shougo/dein.vim') " async plugin manager
  call dein#add('Shougo/neocomplete.vim') " async completion engine
  call dein#add('neomake/neomake') " async :make
  call dein#add('luochen1990/rainbow') " rainbow parens, mainly for clojure
  call dein#add('bling/vim-airline') " neovim does not have a default for the status bar, use the most popular option

  " brought over from plain-old-vim-workflow
  call dein#add('tpope/vim-surround') " exchange delimiters for others, use delimiters for text objects
  call dein#add('tpope/vim-fugitive') " git support, see :Gblame in a tracked file
  call dein#add('thinca/vim-visualstar') " make * search for visual selection rather than finding a text object before
  call dein#add('haya12busa/incsearch.vim') " highlight all matched patterns while writing search keyword
  call dein#add('guns/vim-clojure-static') " highlighting, indentation etc for clojure
  call dein#add('tpope/fireplace.vim') " clojure repl
  call dein#add('morhetz/gruvbox') " pretty colors, dark scheme
  call dein#add('vim-scripts/matchit.zip') " % to not only jump to matching brace, but to if-endif, switch-cases etc
  call dein#add('scrooloose/nerdcommenter') " leader+c+c to toggle comment of visual selection
  call dein#add('ap/vim-css-color') " display actual color of rgb + hex notation in css files
  call dein#add('ctrlpvim/ctrlp.vim') " ctrlp to search through files

  call dein#end()
  call dein#save_state()
endif

filetype plugin indent on
syntax enable

colorscheme gruvbox
set bg=dark

set gdefault
set ignorecase
set smartcase
set foldmethod=indent

let g:rainbow_active = 1

let g:airline_powerline_fonts = 1 " available after dnf install powerline-fonts
