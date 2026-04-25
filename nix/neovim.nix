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
      telescope-ui-select-nvim
      nvim-lspconfig
      blink-cmp
      conform-nvim
      nvim-surround
      which-key-nvim
      (pkgs.vimUtils.buildVimPlugin {
        pname = "vim-lumen";
        version = "unstable";
        src = pkgs.fetchFromGitHub {
          owner = "vimpostor";
          repo = "vim-lumen";
          rev = "master";
          hash = "sha256-SVco2qf7rr2Umgk7B6fEnyebt1zfsaDDCjR9WPTXYqg=";
        };
      })
      firenvim
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
      -- Disable mouse so terminal handles selection (X PRIMARY / middle-click paste)
      vim.opt.mouse = ""
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

      -- visual * searches for the exact selection
      local function visual_star()
        local old = vim.fn.getreg('"')
        vim.cmd('noau normal! y')
        local pat = vim.fn.escape(vim.fn.getreg('"'), [[/\]])
        pat = pat:gsub("\n", [[\n]])
        vim.fn.setreg("/", [[\V]] .. pat)
        vim.fn.setreg('"', old)
        vim.cmd("set hlsearch")
        vim.cmd("normal! n")
      end
      vim.keymap.set("v", "*", visual_star, { silent = true })

      -- visual indent keeps selection
      vim.keymap.set("v", ">", ">gv")
      vim.keymap.set("v", "<", "<gv")

      -- ========================================================================
      -- Restore cursor to last position when reopening a file
      -- ========================================================================
      vim.api.nvim_create_autocmd("BufReadPost", {
        callback = function()
          local mark = vim.api.nvim_buf_get_mark(0, '"')
          local line_count = vim.api.nvim_buf_line_count(0)
          if mark[1] > 0 and mark[1] <= line_count then
            vim.api.nvim_win_set_cursor(0, mark)
          end
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
      -- Transient menu via which-key
      -- ========================================================================
      local wk = require("which-key")
      wk.setup({
        preset = "helix",
        -- delay before popup: long enough that flow-speed operator taps
        -- (,y ,d ,p) don't flash the menu, short enough that a genuine
        -- pause still surfaces it
        delay = 150,
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
            "<leader>" .. t.key,
            function()
              vim.o[t.opt] = not vim.o[t.opt]
              register_toggle_mappings()
            end,
            desc = t.label,
            icon = on and "●" or "○",
          })
        end
        wk.add(m)
      end

      register_toggle_mappings()

      local mappings = {
        { "<leader>f", "<cmd>Telescope find_files<cr>", desc = "find file" },
        { "<leader>v", function()
            vim.cmd("source " .. vim.env.MYVIMRC)
            vim.notify("Reloaded config")
          end, desc = "reload config" },
        { "<leader>r", vim.lsp.buf.rename, desc = "rename symbol" },
      }

      -- Format on save (conform.nvim). For filetypes without an explicit
      -- formatter, fall back to the LSP's own format request — so gopls
      -- formats Go, etc.
      require("conform").setup({
        formatters_by_ft = {
          json  = { "jq" },
          nix   = { "nixfmt" },
          gleam = { "gleam_format" },
          sql   = { "sql_formatter" },
        },
        formatters = {
          sql_formatter = {
            prepend_args = {
              "--language", "sqlite",
              "--config",   '{"keywordCase":"lower","dataTypeCase":"lower","functionCase":"lower"}',
            },
          },
        },
        format_on_save = {
          lsp_format = "fallback",
          timeout_ms = 1000,
        },
      })

      -- Test actions, keyed by filetype
      local testers = {
        rust  = { cmd = "cargo test",   desc = "cargo test" },
        gleam = { cmd = "gleam test",   desc = "gleam test" },
        go    = { cmd = "go test ./...", desc = "go test" },
      }

      -- Run a shell command in a bottom terminal split: ANSI colors render
      -- properly, output is scrollable/yankable, q closes the buffer.
      local function run_in_split(cmd)
        vim.cmd("botright 15split")
        vim.cmd("terminal " .. cmd)
        vim.cmd("stopinsert")
        vim.keymap.set("n", "q", "<cmd>bdelete!<cr>", { buffer = true, silent = true })
      end

      vim.api.nvim_create_autocmd("FileType", {
        pattern = vim.tbl_keys(testers),
        callback = function(ev)
          local t = testers[ev.match]
          if t then
            wk.add({
              {
                "<leader>t",
                function() run_in_split(t.cmd) end,
                desc = t.desc,
                buffer = ev.buf,
              },
            })
          end
        end,
      })

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
        extensions = {
          ["ui-select"] = { require("telescope.themes").get_dropdown({}) },
        },
      })
      require("telescope").load_extension("fzf")
      require("telescope").load_extension("ui-select")

      -- ========================================================================
      -- Completion (blink.cmp): popup auto-shows while typing, nothing
      -- preselected so <CR> only accepts when you've explicitly arrowed onto
      -- an item. <Tab> jumps between snippet placeholders (the ''${1:foo} bits
      -- some LSP servers emit when accepting a function/method).
      -- ========================================================================
      require("blink.cmp").setup({
        keymap = {
          preset = "none",
          ["<CR>"]    = { "accept",           "fallback" },
          ["<Down>"]  = { "select_next",      "fallback" },
          ["<Up>"]    = { "select_prev",      "fallback" },
          ["<Tab>"]   = { "snippet_forward",  "select_and_accept", "fallback" },
          ["<S-Tab>"] = { "snippet_backward", "fallback" },
          ["<Esc>"]   = { "hide",             "fallback" },
        },
        completion = {
          list = { selection = { preselect = false, auto_insert = false } },
          documentation = { auto_show = true },
        },
        signature = { enabled = true },
      })

      -- ========================================================================
      -- LSP
      -- ========================================================================
      vim.diagnostic.config({
        virtual_text = true,
        severity_sort = true,
      })

      -- <Esc> in normal mode closes any visible float (diagnostic popup,
      -- hover, signature help). Default is to close on cursor move, which
      -- is fine — but Esc is a more reliable "go away" reflex.
      vim.keymap.set("n", "<Esc>", function()
        for _, win in ipairs(vim.api.nvim_list_wins()) do
          if vim.api.nvim_win_get_config(win).relative ~= "" then
            pcall(vim.api.nvim_win_close, win, false)
          end
        end
      end, { desc = "close floats" })

      -- Advertise blink.cmp's completion capabilities to every LSP server
      vim.lsp.config("*", {
        capabilities = require("blink.cmp").get_lsp_capabilities(),
      })

      vim.lsp.enable({ "gopls", "gleam", "bashls", "markdown_oxide", "nil_ls", "rust_analyzer", "sqls", "autotools_ls" })

      -- JetBrains-style Ctrl-B: go to definition from a usage; if cursor is
      -- already at the definition's line, list references instead.
      -- Uses show_document directly (rather than going through a qflist) so
      -- the cursor lands on the identifier's column, not column 0. Prefers
      -- targetSelectionRange (the name) over targetRange (whole declaration)
      -- when the server returns a LocationLink.
      local function smart_goto()
        local clients = vim.lsp.get_clients({ bufnr = 0, method = "textDocument/definition" })
        if #clients == 0 then return end
        local client = clients[1]
        local word = vim.fn.expand("<cword>")
        local params = vim.lsp.util.make_position_params(0, client.offset_encoding)
        client:request("textDocument/definition", params, function(err, result)
          if err or not result then return end
          local locations = vim.islist(result) and result or { result }
          if #locations == 0 then return end
          local first = locations[1]
          local target_uri = first.uri or first.targetUri
          local target_rng = first.targetSelectionRange or first.range or first.targetRange
          local cur = vim.api.nvim_win_get_cursor(0)
          if target_uri == vim.uri_from_bufnr(0)
              and target_rng.start.line == cur[1] - 1 then
            require("telescope.builtin").lsp_references()
            return
          end
          vim.lsp.util.show_document(
            { uri = target_uri, range = target_rng },
            client.offset_encoding,
            { focus = true }
          )
          -- Workaround: gleam-lsp (and possibly others) return a Location
          -- whose range covers the whole declaration line/block with
          -- character=0 and no targetSelectionRange — so we land on the
          -- start of the line, not on the identifier. If the symbol we
          -- jumped FROM appears on the landing line, reposition onto it.
          if word ~= "" and not first.targetSelectionRange then
            local landed = vim.api.nvim_win_get_cursor(0)
            local line = vim.api.nvim_buf_get_lines(0, landed[1] - 1, landed[1], false)[1]
            if line then
              local at = line:sub(landed[2] + 1, landed[2] + #word)
              if at ~= word then
                local col = line:find(word, 1, true)
                if col then
                  vim.api.nvim_win_set_cursor(0, { landed[1], col - 1 })
                end
              end
            end
          end
        end)
      end

      vim.api.nvim_create_autocmd("LspAttach", {
        callback = function(ev)
          local map = function(k, fn, desc)
            vim.keymap.set("n", k, fn, { buffer = ev.buf, desc = desc })
          end
          map("<leader>a", vim.lsp.buf.code_action,                                       "code action")
          map("K",         vim.lsp.buf.hover,                                             "hover")
          map("<C-b>",     smart_goto,                                                    "goto def / list refs")
          map("<leader>e", vim.diagnostic.goto_next,                                      "next diagnostic")
          map("<leader>l", function()
            -- ~25 cols reserved for borders, kind column, and padding
            local sym_width = math.max(25, vim.o.columns - 25)
            require("telescope.builtin").lsp_document_symbols({ symbol_width = sym_width })
          end, "list symbols")
        end,
      })

      -- ========================================================================
      -- Firenvim (browser textarea editing)
      -- ========================================================================
      -- Don't auto-takeover textareas; require Ctrl+E to opt in
      vim.g.firenvim_config = {
        globalSettings = {
          alt = "all",
        },
        localSettings = {
          [".*"] = {
            takeover = "never",
          },
        },
      }

      if vim.g.started_by_firenvim then
        -- Larger font for readability in browser
        vim.o.guifont = "monospace:h14"

        -- Don't show statusline/cmdline clutter in the small textarea
        vim.o.laststatus = 0

        -- Auto-sync buffer back to textarea on every change
        vim.api.nvim_create_autocmd({"TextChanged", "InsertLeave"}, {
          callback = function()
            if vim.bo.buftype == "" then
              vim.cmd("silent write")
            end
          end,
        })
      end
    '';
  };
}
