{ pkgs, ... }:
{
  programs.neovim = {
    enable = true;
    vimAlias = true;
    viAlias = true;
    withPython3 = false;
    withRuby = false;

    plugins = with pkgs.vimPlugins; [
      plenary-nvim
      nvim-web-devicons
      telescope-fzf-native-nvim
      telescope-nvim
      nvim-surround
      which-key-nvim
    ];

    initLua = ''
      -- ========================================================================
      -- Basics
      -- ========================================================================
      vim.opt.modeline = true
      vim.opt.modelines = 5
      vim.opt.incsearch = true
      vim.opt.ignorecase = true
      vim.opt.smartcase = true
      vim.opt.number = false
      vim.opt.cursorline = true
      vim.opt.autoindent = true
      vim.opt.foldmethod = "indent"
      vim.opt.foldlevel = 20
      vim.opt.listchars = {
        tab = "⇤–⇥",
        space = "·",
        trail = "·",
        precedes = "⇠",
        extends = "⇢",
        nbsp = "×",
      }

      vim.g.mapleader = ","

      vim.api.nvim_set_hl(0, "CursorLine", { underline = true })

      -- Transparent background with appropriate foreground for light/dark.
      -- Neovim's default colorscheme pairs its fg with its own bg, so when we
      -- make bg transparent we also need to fix fg to match the terminal.
      local transparent_groups = { "Normal", "NormalNC", "NormalFloat", "SignColumn", "EndOfBuffer", "LineNr", "FoldColumn" }
      local function apply_transparent_theme()
        local is_light = vim.o.background == "light"
        for _, group in ipairs(transparent_groups) do
          vim.cmd("highlight " .. group .. " guibg=NONE ctermbg=NONE")
        end
        -- Fix Normal fg to contrast with the terminal background
        if is_light then
          vim.cmd("highlight Normal guifg=#000000 ctermfg=0")
        else
          vim.cmd("highlight Normal guifg=#d0d0d0 ctermfg=252")
        end
      end
      vim.api.nvim_create_autocmd("ColorScheme", { callback = apply_transparent_theme })
      vim.api.nvim_create_autocmd("OptionSet", {
        pattern = "background",
        callback = apply_transparent_theme,
      })
      apply_transparent_theme()

      -- ========================================================================
      -- Keymaps
      -- ========================================================================

      -- yank/delete/paste via register z, so paste-over doesn't clobber the buffer
      vim.keymap.set("n", "<leader>y", '"zy')
      vim.keymap.set("v", "<leader>y", '"zy')
      vim.keymap.set("n", "<leader>d", '"zd')
      vim.keymap.set("v", "<leader>d", '"zd')
      vim.keymap.set("n", "<leader>p", '"zp')
      vim.keymap.set("v", "<leader>p", '"zp')

      -- j/k move by visual line when no count
      vim.keymap.set("n", "k", function()
        return vim.v.count == 0 and "gk" or "k"
      end, { expr = true })
      vim.keymap.set("n", "j", function()
        return vim.v.count == 0 and "gj" or "j"
      end, { expr = true })

      -- visual indent keeps selection
      vim.keymap.set("v", ">", ">gv")
      vim.keymap.set("v", "<", "<gv")

      -- ========================================================================
      -- Nix formatting on save
      -- ========================================================================
      vim.api.nvim_create_autocmd("BufWritePre", {
        pattern = "*.nix",
        callback = function()
          local buf = vim.api.nvim_get_current_buf()
          local filename = vim.api.nvim_buf_get_name(buf)
          if filename == "" then return end
          vim.cmd("silent !nixfmt " .. vim.fn.shellescape(filename))
          vim.cmd("edit")
        end,
      })

      -- ========================================================================
      -- Markdown folds on headers
      -- ========================================================================
      function _G.MarkdownFoldLevel()
        local line = vim.fn.getline(vim.v.lnum)
        for level = 6, 1, -1 do
          if line:match("^" .. string.rep("#", level) .. " ") then
            return ">" .. level
          end
        end
        return "="
      end

      vim.api.nvim_create_autocmd("BufEnter", {
        pattern = "*.md",
        callback = function()
          vim.opt_local.foldexpr = "v:lua.MarkdownFoldLevel()"
          vim.opt_local.foldmethod = "expr"
        end,
      })

      -- ========================================================================
      -- Trailing space highlight (only when :set list is on)
      -- ========================================================================
      vim.api.nvim_set_hl(0, "TrailingSpace", { bg = "#ff0000", fg = "#ffffff", ctermbg = "red", ctermfg = "white" })

      local trailing_ns = vim.api.nvim_create_namespace("trailing_space")

      local function update_trailing_spaces()
        local bufs = vim.api.nvim_list_bufs()
        for _, buf in ipairs(bufs) do
          if vim.api.nvim_buf_is_loaded(buf) then
            vim.api.nvim_buf_clear_namespace(buf, trailing_ns, 0, -1)
            if vim.wo.list then
              local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
              for i, line in ipairs(lines) do
                local s, e = line:find("%s+$")
                if s then
                  vim.api.nvim_buf_add_highlight(buf, trailing_ns, "TrailingSpace", i - 1, s - 1, e)
                end
              end
            end
          end
        end
      end

      vim.api.nvim_create_autocmd("OptionSet", {
        pattern = "list",
        callback = update_trailing_spaces,
      })

      -- ========================================================================
      -- Highlight groups
      -- ========================================================================
      vim.api.nvim_set_hl(0, "Pmenu", { ctermbg = 237, ctermfg = 252, bg = "#3a3a3a", fg = "#d0d0d0" })
      vim.api.nvim_set_hl(0, "PmenuSel", { ctermbg = 241, ctermfg = 231, bold = true, bg = "#626262", fg = "#ffffff" })
      vim.api.nvim_set_hl(0, "PmenuSbar", { ctermbg = 238, bg = "#444444" })
      vim.api.nvim_set_hl(0, "PmenuThumb", { ctermbg = 248, bg = "#a8a8a8" })

      -- Transient menu highlight groups (used by which-key overrides if needed)
      vim.api.nvim_set_hl(0, "TransientMenu", { ctermbg = 236, ctermfg = 252, bg = "#303030", fg = "#d0d0d0" })
      vim.api.nvim_set_hl(0, "TransientMenuBorder", { ctermbg = 236, ctermfg = 245, bg = "#303030", fg = "#8a8a8a" })
      vim.api.nvim_set_hl(0, "TransientOn", { ctermfg = 108, fg = "#87af87" })
      vim.api.nvim_set_hl(0, "TransientOff", { ctermfg = 131, fg = "#af5f5f" })
      vim.api.nvim_set_hl(0, "TransientMnemonic", { ctermbg = 236, ctermfg = 216, bg = "#303030", fg = "#ffaf87" })
      vim.api.nvim_set_hl(0, "TransientHeader", { ctermbg = 236, ctermfg = 252, bold = true, bg = "#303030", fg = "#d0d0d0" })

      -- ========================================================================
      -- Transient menu via which-key
      -- ========================================================================
      local wk = require("which-key")
      wk.setup({
        preset = "helix",
        delay = 0,
      })

      -- Toggle helper: toggles opt, then refreshes which-key descriptions
      local toggles = {
        { key = "n", opt = "number", label = "line numbers" },
        { key = "c", opt = "list",   label = "chars display" },
        { key = "w", opt = "wrap",   label = "wrap" },
        { key = "s", opt = "spell",  label = "spell" },
      }

      local function register_toggle_mappings()
        local m = {}
        for _, t in ipairs(toggles) do
          local on = vim.o[t.opt]
          table.insert(m, {
            "<leader><leader>" .. t.key,
            function()
              vim.o[t.opt] = not vim.o[t.opt]
              register_toggle_mappings()
            end,
            desc = t.label,
            icon = on
              and { icon = "●", hl = "TransientOn" }
              or  { icon = "○", hl = "TransientOff" },
          })
        end
        wk.add(m)
      end

      register_toggle_mappings()

      -- Base menu under ,,
      local mappings = {
        { "<leader><leader>", group = "menu" },
        { "<leader><leader>f", "<cmd>Telescope find_files<cr>", desc = "find file" },
        { "<leader><leader>v", function()
            vim.cmd("source " .. vim.env.MYVIMRC)
            vim.notify("Reloaded config")
          end, desc = "reload config" },
      }

      -- mdlink column from D_MDLINK_DATA env var
      local mdlink_data = vim.env.D_MDLINK_DATA
      if mdlink_data and mdlink_data ~= "" then
        local ok, data = pcall(vim.json.decode, mdlink_data)
        if ok and data and data.items then
          local used_keys = {}

          table.insert(mappings, { "<leader><leader>t", group = data.header or "Tags" })

          for _, item in ipairs(data.items) do
            local label = item.name or item.path
            local key = nil

            -- Try to find a key from the label characters
            for i = 1, #label do
              local char = label:sub(i, i):lower()
              if char:match("[a-z0-9]") and not used_keys[char] then
                key = char
                break
              end
            end

            -- Fallback: find any unused key
            if not key then
              for c in ("abcdefghijklmnopqrstuvwxyz0123456789"):gmatch(".") do
                if not used_keys[c] then
                  key = c
                  break
                end
              end
            end

            if key then
              used_keys[key] = true
              local link = "[" .. label .. "](d:tag:" .. item.path .. ")"
              table.insert(mappings, {
                "<leader><leader>t" .. key,
                function()
                  vim.api.nvim_put({ link }, "c", true, true)
                end,
                desc = label,
              })
            end
          end
        end
      end

      wk.add(mappings)

      -- ========================================================================
      -- Surround (ys, cs, ds)
      -- ========================================================================
      require("nvim-surround").setup()

      -- ========================================================================
      -- Telescope setup
      -- ========================================================================
      require("telescope").setup({
        defaults = {
          layout_strategy = "vertical",
        },
      })
      require("telescope").load_extension("fzf")
    '';
  };
}
