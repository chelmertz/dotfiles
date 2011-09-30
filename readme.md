#usage
##installation
```bash
#install xcode

#install brew

#install brew formulas

mkdir -p ~/.config/dotfiles && cd ~/.config/dotfiles
git clone https://chelmertz@github.com/chelmertz/dotfiles.git
git submodule init
git submodule update

#symlink files to ~: http://www.commandlinefu.com/commands/view/1225/symlink-all-files-from-a-base-directory-to-a-target-directory
```

##updating
```bash
git pull origin master
./update-submodules.sh
```

After the repository is updated, make sure symlinks follow the possibly new filenames.

#good to have
 - zsh + oh-my-zsh
 - homebrew with ctags
 - gimli (md to pdf): https://github.com/walle/gimli

# good to know
 - ~/.zsh-local is ignored and contains your local/private aliases etc, useful for work stations

##todo
 - support project specific aliases
 - check if vim plugin is loaded before mapping
 - find git repos of .lvimrc (vim script 1408)
 - homebrew formulas

# vim buffers
- `<leader>be` - list buffers (enter to enter, esc to leave, d to delete)
- `:bd` - quit current buffer
- `:e <filename>` - open <filename> in new buffer

# vim plugins

*Assuming `setleader=,`*

- ack-vim - `,a <keyword>` searches recursively and open results in buffer
- gist-vim - `:Gist` posts buffer as public gist
- matchit.zip - *`%`matches html tags too
- nerdcommenter - `,cc` comments visual selection, `,ci` inverts already commented lines
- quicksilver.vim - `,q` autocompletes your file browser
- supertab - `<tab>` for autocompletion (like `<C-n>`)
- syntastic - syntax check on write
- vim-bufexplorer - `,be` to switch between buffers, `,bs` hori. split, `,bv` vert. split
- vim-conque - `,bs` for bash in a split window
- vim-easymotion - `,w` for shortcuts within view
- vim-extradite - `,gs` shows commit log for current file
- vim-fugitive - git support, `:Gdiff` for example
- vim-json - json syntax validation & highlight
- vim-markdown - markdown syntax validation & highlight
- vim-pathogen - place plugins in `.vim/bundle/` to avoid clutter
- vim-repeat - use `.` to repeat plugin actions
- vim-space - repeat commands with `<space>`
- vim-surround - `cs"'` on `"hello"` gives `'hello'`, `ysiw'` on `Hello` gives `'Hello'`, `ds'` on `'Hello'` gives `Hello`, `S(` on visually selected `Hello` gives `( Hello )`
- vim-task - save a todo.txt,*.task and use `,w` on a line starting with `-` to toggle its status
- VisIncr - on visual block selection, `:I` generates list (`:II` for padding on the left side)
- zencoding-vim - html generation with few keystrokes, `<C-y>,` on `html:5x` generates html5 boilerplate (where x is cursor position)
