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
      vim-test
      nvim-surround
      which-key-nvim
      undotree
      csvview-nvim
      rainbow_csv
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
      vim.opt.undofile = true
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
      -- Prose check (markdown only)
      -- Surfaces weasel words / passive voice / duplicate words from
      -- ~/.local/bin/prose-check as LSP-style diagnostics. With the existing
      -- vim.diagnostic.config({ virtual_text = true }) below, hits render
      -- inline next to the offending line — same UX as LSP warnings.
      -- Runs async on BufReadPost + BufWritePost. Manual trigger via :ProseCheck.
      -- ========================================================================
      local prose_ns = vim.api.nvim_create_namespace("prose_check")
      local prose_msg = {
        weasel    = "weasel word",
        passive   = "passive voice",
        duplicate = "duplicate words",
      }

      local function prose_check_run(bufnr)
        bufnr = bufnr or vim.api.nvim_get_current_buf()
        local file = vim.api.nvim_buf_get_name(bufnr)
        if file == "" or vim.fn.filereadable(file) == 0 then return end
        if vim.fn.executable("prose-check") == 0 then return end

        vim.system({ "prose-check", file }, { text = true }, function(result)
          local diagnostics = {}
          -- Format: "[rule] /path:lineno:rest-of-line". Use lazy .- for the
          -- path part so colons in paths (rare) don't break parsing.
          for line in (result.stderr or ""):gmatch("[^\n]+") do
            local rule, lnum, rest = line:match("^%[(%w+)%] .-:(%d+):(.*)$")
            if rule and lnum then
              -- duplicates are almost always real prose bugs → WARN (yellow).
              -- weasel / passive are judgment calls → HINT (subtle, gray).
              local sev = (rule == "duplicate")
                  and vim.diagnostic.severity.WARN
                  or  vim.diagnostic.severity.HINT
              local msg = (rule == "duplicate")
                  and ("duplicate: " .. rest)
                  or  (prose_msg[rule] or rule)
              table.insert(diagnostics, {
                bufnr    = bufnr,
                lnum     = tonumber(lnum) - 1,
                col      = 0,
                severity = sev,
                source   = "prose-check",
                message  = msg,
              })
            end
          end
          vim.schedule(function()
            if vim.api.nvim_buf_is_valid(bufnr) then
              vim.diagnostic.set(prose_ns, bufnr, diagnostics)
            end
          end)
        end)
      end

      vim.api.nvim_create_autocmd({ "BufReadPost", "BufWritePost" }, {
        pattern = "*.md",
        callback = function(ev) prose_check_run(ev.buf) end,
      })

      vim.api.nvim_create_user_command("ProseCheck", function()
        prose_check_run()
      end, { desc = "Run prose-check on current buffer" })

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
        { "<leader>f", function()
            require("telescope.builtin").find_files({
              prompt_title = "Find Files  •  <C-v> vsplit  <C-x> split  <C-t> tab",
              find_command = { "rg", "--files", "--hidden", "--glob=!.git/" },
            })
          end, desc = "find file" },
        { "<leader>v", function()
            vim.cmd("source " .. vim.env.MYVIMRC)
            vim.notify("Reloaded config")
          end, desc = "reload config" },
        { "<leader>r", vim.lsp.buf.rename, desc = "rename symbol" },
        { "<leader>b", function()
            require("telescope.builtin").buffers({
              prompt_title = "Buffers  •  <Del> kill  •  <C-v> vsplit  <C-x> split  <C-t> tab",
              ignore_current_buffer = true,
              sort_mru = true,
            })
          end, desc = "buffers" },
        { "<leader>g", function()
            -- empty() sorter preserves gopls's ranking. The default fuzzy
            -- sorter scores against the full display string (type prefix
            -- included), which buries exact name matches like SchedulePush
            -- under near-misses like CancelScheduledPush.
            require("telescope.builtin").lsp_dynamic_workspace_symbols({
              prompt_title = "Workspace Symbols",
              sorter = require("telescope.sorters").empty(),
            })
          end, desc = "workspace symbols" },
        { "<leader>G", function()
            require("telescope.builtin").live_grep({
              prompt_title = "Live Grep  •  <C-v> vsplit  <C-x> split  <C-t> tab",
              additional_args = function() return { "--hidden", "--glob=!.git/" } end,
            })
          end, desc = "live grep" },
        { "<leader>u", "<cmd>UndotreeToggle<cr>", desc = "undo tree" },
        -- Prose-check refresh. Auto-runs on save/open of *.md, but useful
        -- to retrigger manually if you've cleaned up and want to confirm.
        { "<leader>P", prose_check_run, desc = "prose check (md)" },
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

      -- Test running via vim-test. <leader>t is smart: nearest if you're
      -- in a test file, whole-project suite otherwise. <leader>T always
      -- runs the current file. Output goes to a 15-row terminal split.
      vim.g["test#strategy"] = "neovim"
      vim.g["test#neovim#term_position"] = "botright 15"
      -- Land in normal mode (not terminal-job mode) so keys navigate the
      -- buffer instead of triggering nvim's "press any key to dismiss".
      vim.g["test#neovim#start_normal"] = 1

      local function smart_test()
        local path = vim.fn.expand("%:p")
        local is_test = path:match("_test%.[^/]+$") or path:match("/tests?/")
        -- Rust: tests live in the same file under #[cfg(test)] / #[test]
        if not is_test and vim.bo.filetype == "rust" then
          for _, line in ipairs(vim.api.nvim_buf_get_lines(0, 0, -1, false)) do
            if line:match("#%[test%]") or line:match("#%[cfg%(test%)%]") then
              is_test = true
              break
            end
          end
        end
        if is_test then
          vim.cmd("TestNearest")
        else
          vim.cmd("TestSuite")
        end
      end

      vim.api.nvim_create_autocmd("FileType", {
        pattern = { "rust", "gleam", "go" },
        callback = function(ev)
          wk.add({
            { "<leader>t", smart_test,            desc = "test (nearest or suite)", buffer = ev.buf },
            { "<leader>T", "<cmd>TestFile<cr>",   desc = "test file",               buffer = ev.buf },
          })
        end,
      })

      -- q closes vim-test's terminal output buffer
      vim.api.nvim_create_autocmd("TermOpen", {
        callback = function()
          vim.keymap.set("n", "q", "<cmd>bdelete!<cr>", { buffer = true, silent = true })
        end,
      })

      wk.add(mappings)

      -- ========================================================================
      -- Surround (ys, cs, ds)
      -- ========================================================================
      require("nvim-surround").setup()

      -- ========================================================================
      -- CSV: csvview aligns columns + keeps the header pinned (defaults are
      -- already sticky_header=true, header_lnum=auto). rainbow_csv gives
      -- column tinting and RBQL via :Select. RBQL needs python3 on PATH.
      -- ========================================================================
      require("csvview").setup({
        view = { display_mode = "border" },  -- draw │ between columns
      })

      -- Returns (col_idx, value, header_text) for the field under the cursor,
      -- or nil if csvview isn't attached / cursor isn't on a field.
      local function csv_cell()
        local ok, util = pcall(require, "csvview.util")
        if not ok then return end
        local ok2, cur = pcall(util.get_cursor)
        if not ok2 or cur.kind ~= "field" then return end
        local col = cur.pos[2]
        -- Naive header lookup: split line 1 on the buffer's delimiter. Doesn't
        -- handle quoted commas — refine if it bites.
        local delim = vim.bo.filetype == "tsv" and "\t" or ","
        local header_line = vim.api.nvim_buf_get_lines(0, 0, 1, false)[1] or ""
        local hdr = vim.split(header_line, delim, { plain = true })[col]
        return col, cur.text, hdr or ("col" .. col)
      end

      -- Count lines in a buffer, ignoring a trailing empty line. RBQL writes
      -- one to its result buffer; source files often have one too.
      local function csv_records(buf)
        buf = buf or 0
        local n = vim.api.nvim_buf_line_count(buf)
        local last = vim.api.nvim_buf_get_lines(buf, n - 1, n, false)[1]
        if last == "" then n = n - 1 end
        return n
      end

      -- Run an RBQL Select and notify match/total. `expr` is the WHERE clause.
      local function csv_run_select(expr, label)
        local total = csv_records(0) - 1  -- minus header row
        vim.cmd(string.format([[Select * where %s]], expr))
        vim.defer_fn(function()
          vim.notify(string.format("%s : %d / %d rows",
            label, csv_records(0), total))
        end, 400)
      end

      -- :CsvFilter — prompt for column + substring (used outside the cursor
      -- flow, e.g. when you don't have csvview attached).
      vim.api.nvim_create_user_command("CsvFilter", function()
        vim.ui.input({ prompt = "Column (1-based): " }, function(col)
          if not col or col == "" then return end
          vim.ui.input({ prompt = "Substring in a" .. col .. ": " }, function(val)
            if not val then return end
            local esc = val:gsub('"', '\\"')
            csv_run_select(
              string.format([[a%s like "%%%s%%"]], col, esc),
              string.format("a%s ~ %q", col, val))
          end)
        end)
      end, {})

      -- Per-buffer setup: enable view, remap PageUp/Down to half-page (so the
      -- cursor doesn't overlap and hide the sticky header), bind ,C submenu.
      vim.api.nvim_create_autocmd("FileType", {
        pattern = { "csv", "tsv" },
        callback = function(ev)
          vim.cmd("CsvViewEnable")

          local opts = { buffer = ev.buf, silent = true }
          vim.keymap.set("n", "<PageDown>", "<C-d>", opts)
          vim.keymap.set("n", "<PageUp>",   "<C-u>", opts)

          require("which-key").add({
            { "<leader>C",  group = "csv", buffer = ev.buf },
            { "<leader>Cv", "<cmd>CsvViewToggle<cr>",
              desc = "toggle aligned view + sticky header", buffer = ev.buf },
            { "<leader>Cp", "<cmd>CsvFilter<cr>",
              desc = "filter rows: ask for column + substring", buffer = ev.buf },
            { "<leader>Ch", function()
                local col, val, hdr = csv_cell()
                if not col then return vim.notify("not on a CSV field") end
                vim.notify(string.format("col %d (%s) = %q", col, hdr, val))
              end,
              desc = "show this cell's column name + value", buffer = ev.buf },
            { "<leader>Cf", function()
                local col, val, hdr = csv_cell()
                if not col then return vim.notify("not on a CSV field") end
                local esc = val:gsub('"', '\\"')
                csv_run_select(
                  string.format([[a%d == "%s"]], col, esc),
                  string.format("%s == %q", hdr, val))
              end,
              desc = "filter rows where this column equals this cell", buffer = ev.buf },
            { "<leader>Cl", function()
                local col, val, hdr = csv_cell()
                if not col then return vim.notify("not on a CSV field") end
                local esc = val:gsub('"', '\\"')
                csv_run_select(
                  string.format([[a%d like "%%%s%%"]], col, esc),
                  string.format("%s ~ %q", hdr, val))
              end,
              desc = "filter rows where this column contains this cell", buffer = ev.buf },
          })
        end,
      })

      -- ========================================================================
      -- Telescope setup
      -- ========================================================================
      require("telescope").setup({
        defaults = {
          layout_strategy = "vertical",
          vimgrep_arguments = {
            "rg",
            "--color=never",
            "--no-heading",
            "--with-filename",
            "--line-number",
            "--column",
            "--smart-case",
            "--hidden",
            "--glob=!**/.git/**",
          },
        },
        pickers = {
          buffers = {
            mappings = {
              n = {
                ["dd"]      = require("telescope.actions").delete_buffer,
                ["<Del>"]   = require("telescope.actions").delete_buffer,
              },
              i = {
                ["<Del>"]   = require("telescope.actions").delete_buffer,
              },
            },
          },
        },
        extensions = {
          ["ui-select"] = { require("telescope.themes").get_dropdown({}) },
        },
      })
      require("telescope").load_extension("fzf")
      require("telescope").load_extension("ui-select")

      -- ========================================================================
      -- Completion (blink.cmp): popup auto-shows while typing, nothing
      -- preselected. <CR> picks the first item if none arrowed onto, otherwise
      -- accepts the selected one; if the menu isn't open it inserts a newline.
      -- <Tab> jumps between snippet placeholders (the ''${1:foo} bits some LSP
      -- servers emit when accepting a function/method).
      -- ========================================================================
      require("blink.cmp").setup({
        keymap = {
          preset = "none",
          ["<CR>"]    = { "select_and_accept", "fallback" },
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

      -- yamlls: schemastore stays on (so .github/workflows/*.yml, gitlab-ci,
      -- docker-compose, etc. get auto-bound). Explicitly bind OpenAPI 3.0 to
      -- our spec filenames — schemastore's openapi entry only globs
      -- `openapi.yaml`, but our specs are named api.yml.
      vim.lsp.config("yamlls", {
        settings = {
          yaml = {
            schemas = {
              ["https://raw.githubusercontent.com/OAI/OpenAPI-Specification/main/schemas/v3.0/schema.yaml"] = {
                "**/api.yml", "**/api.yaml",
                "**/openapi.yml", "**/openapi.yaml",
              },
            },
          },
        },
      })

      -- ruff handles lint + format; defer hover to basedpyright (richer type info).
      vim.lsp.config("ruff", {
        on_attach = function(client)
          client.server_capabilities.hoverProvider = false
        end,
      })

      vim.lsp.enable({ "gopls", "gleam", "bashls", "markdown_oxide", "nil_ls", "rust_analyzer", "sqls", "autotools_ls", "yamlls", "basedpyright", "ruff" })

      -- :LspRestart — stop clients attached to current buffer and re-attach.
      -- nvim-lspconfig used to provide this; with the vim.lsp.config API we
      -- roll our own.
      vim.api.nvim_create_user_command("LspRestart", function()
        local clients = vim.lsp.get_clients({ bufnr = 0 })
        for _, c in ipairs(clients) do
          vim.lsp.stop_client(c.id)
        end
        vim.cmd.edit()
      end, { desc = "restart LSP clients on current buffer" })

      -- yaml-language-server's getDefinition only handles YAML anchors
      -- (&foo/*foo), not JSON $refs. So we resolve $ref ourselves: split on
      -- '#', resolve the file part relative to the current buffer's dir,
      -- then walk the JSON pointer by treating each segment as a key under
      -- the previous (assumes 2-space indentation, which OpenAPI uses).
      -- Returns true if cursor was on a $ref line (regardless of jump
      -- success), so smart_goto knows to skip the LSP path.
      local function try_yaml_ref_goto()
        if vim.bo.filetype ~= "yaml" then return false end
        local line = vim.api.nvim_get_current_line()
        local ref = line:match([[%$ref:%s*['"]?([^'"%s]+)]])
        if not ref then return false end

        local file_part, fragment = ref:match("^([^#]*)#?(.*)$")
        file_part = file_part or ""
        fragment  = fragment or ""

        if file_part ~= "" then
          local cur_dir = vim.fn.expand("%:p:h")
          local abs = vim.fn.fnamemodify(cur_dir .. "/" .. file_part, ":p")
          if vim.fn.filereadable(abs) == 0 then
            vim.notify("yaml-ref: file not readable: " .. abs, vim.log.levels.WARN)
            return true
          end
          vim.cmd("edit " .. vim.fn.fnameescape(abs))
        end

        if fragment == "" or fragment == "/" then
          vim.api.nvim_win_set_cursor(0, { 1, 0 })
          return true
        end

        -- JSON Pointer: split on '/', unescape ~1 -> /, ~0 -> ~
        local segments = {}
        for seg in fragment:gmatch("[^/]+") do
          seg = seg:gsub("~1", "/"):gsub("~0", "~")
          table.insert(segments, seg)
        end

        local lines = vim.api.nvim_buf_get_lines(0, 0, -1, false)
        local from = 1
        local found_line = nil
        for level, seg in ipairs(segments) do
          local indent = string.rep("  ", level - 1)
          local pattern = "^" .. indent .. vim.pesc(seg) .. ":"
          found_line = nil
          for i = from, #lines do
            if lines[i]:match(pattern) then
              found_line = i
              from = i + 1
              break
            end
          end
          if not found_line then
            vim.notify("yaml-ref: segment not found: " .. seg, vim.log.levels.WARN)
            return true
          end
        end
        local final_indent = string.rep("  ", #segments - 1)
        vim.api.nvim_win_set_cursor(0, { found_line, #final_indent })
        vim.cmd("normal! zz")
        return true
      end

      -- Inverse of try_yaml_ref_goto: when cursor is on a top-level OpenAPI
      -- component definition (components/<type>/<name>), search the workspace
      -- for $refs pointing to it. Mirrors smart_goto's "press C-b at the def
      -- to list refs" behavior, since yamlls returns nothing for these keys.
      local function try_yaml_ref_back()
        if vim.bo.filetype ~= "yaml" then return false end
        if vim.api.nvim_get_current_line():match("%$ref:") then return false end

        -- Build the JSON pointer of the cursor's key by walking back through
        -- ancestors with smaller indentation. Assumes 2-space indentation.
        local lines = vim.api.nvim_buf_get_lines(0, 0, -1, false)
        local cur_lnum = vim.api.nvim_win_get_cursor(0)[1]
        local cur_indent, cur_key = (lines[cur_lnum] or ""):match("^( *)([^:%s][^:]*):")
        if not cur_key then return false end

        local segments = { cur_key }
        local indent = #cur_indent
        for i = cur_lnum - 1, 1, -1 do
          local lead, key = lines[i]:match("^( *)([^:%s][^:]*):")
          if lead and #lead < indent then
            table.insert(segments, 1, key)
            indent = #lead
            if indent == 0 then break end
          end
        end

        -- Only trigger for components/<type>/<name>. Skip nested fields,
        -- root keys, paths/method nodes, etc.
        if #segments ~= 3 or segments[1] ~= "components" then return false end
        local pointer = "#/" .. table.concat(segments, "/")

        -- Strip leading '#' from the search so this matches both same-file
        -- (#/...) and cross-file (path.yml#/...) refs.
        require("telescope.builtin").grep_string({
          prompt_title = "$ref usages of " .. pointer,
          search = pointer:sub(2),
          additional_args = function()
            return { "--hidden", "--glob=!.git/" }
          end,
        })
        return true
      end

      -- Returns 1 if the entry's path/source line looks like noise (tests,
      -- mocks/fakes/noop stand-ins, generated build artifacts), 0 otherwise.
      -- Matches both filename and source text — the latter catches cases like
      -- `type noopPusher struct{}` where the path itself is innocent.
      local function entry_is_low_priority(filename, text)
        local s = ((filename or "") .. " " .. (text or "")):lower()
        if s:match("test") or s:match("mock") or s:match("fake") or s:match("noop")
            or s:match("^build/") or s:match("/build/") then
          return 1
        end
        return 0
      end

      -- LSP references picker: demotes test-adjacent entries so production
      -- usages float to the top. Telescope preserves insertion order with an
      -- empty prompt, so we just pre-sort items.
      local function smart_references()
        local clients = vim.lsp.get_clients({ bufnr = 0, method = "textDocument/references" })
        if #clients == 0 then return end
        local client = clients[1]
        local params = vim.lsp.util.make_position_params(0, client.offset_encoding)
        params.context = { includeDeclaration = true }
        client:request("textDocument/references", params, function(err, result)
          if err or not result or vim.tbl_isempty(result) then return end
          local items = vim.lsp.util.locations_to_items(result, client.offset_encoding)
          table.sort(items, function(a, b)
            local fa = entry_is_low_priority(a.filename, a.text)
            local fb = entry_is_low_priority(b.filename, b.text)
            if fa ~= fb then return fa < fb end
            return (a.filename or "") < (b.filename or "")
          end)

          local pickers = require("telescope.pickers")
          local finders = require("telescope.finders")
          local conf = require("telescope.config").values
          local make_entry = require("telescope.make_entry")

          pickers.new({}, {
            prompt_title = "LSP References",
            finder = finders.new_table({
              results = items,
              entry_maker = make_entry.gen_from_quickfix({}),
            }),
            sorter = conf.generic_sorter({}),
            previewer = conf.qflist_previewer({}),
          }):find()
        end)
      end

      -- JetBrains-style Ctrl-B: go to definition from a usage; if cursor is
      -- already at the definition's line, list references instead.
      -- Uses show_document directly (rather than going through a qflist) so
      -- the cursor lands on the identifier's column, not column 0. Prefers
      -- targetSelectionRange (the name) over targetRange (whole declaration)
      -- when the server returns a LocationLink.
      local function smart_goto()
        if try_yaml_ref_goto() then return end
        if try_yaml_ref_back() then return end
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
            smart_references()
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

      -- LSP document symbols picker: pre-sorts by visibility (public first)
      -- then alphabetically, with a ● / ○ visibility decoration. Custom
      -- picker because telescope's lsp_document_symbols ignores tiebreak.
      local function smart_list_symbols()
        local clients = vim.lsp.get_clients({ bufnr = 0, method = "textDocument/documentSymbol" })
        if #clients == 0 then return end
        local client = clients[1]
        local params = { textDocument = vim.lsp.util.make_text_document_params(0) }
        client:request("textDocument/documentSymbol", params, function(err, result)
          if err or not result then return end
          local source = vim.api.nvim_buf_get_lines(0, 0, -1, false)
          local ft = vim.bo.filetype

          local function check_pub(src_line, name)
            if src_line:match("^%s*pub[%s(]") then return true end
            if ft == "go" and name and name:sub(1, 1):match("%u") then return true end
            return false
          end

          local items = {}
          local function flatten(syms)
            for _, s in ipairs(syms) do
              local rng = (s.location and s.location.range) or s.selectionRange or s.range
              local ln = rng.start.line + 1
              table.insert(items, {
                name = s.name,
                kind = vim.lsp.protocol.SymbolKind[s.kind] or "Unknown",
                lnum = ln,
                col = rng.start.character + 1,
                pub = check_pub(source[ln] or "", s.name),
              })
              if s.children then flatten(s.children) end
            end
          end
          flatten(result)

          -- Reversed: telescope renders the first input item nearest the
          -- prompt (bottom). We want alpha to read top-down, so feed it
          -- private-first and z→a within each visibility group.
          table.sort(items, function(a, b)
            if a.pub ~= b.pub then return not a.pub end
            return a.name > b.name
          end)

          local pickers = require("telescope.pickers")
          local finders = require("telescope.finders")
          local conf = require("telescope.config").values
          local entry_display = require("telescope.pickers.entry_display")

          local displayer = entry_display.create({
            separator = " ",
            items = {
              { width = 1 },          -- ● / ○
              { width = 10 },         -- kind
              { remaining = true },   -- symbol name
            },
          })

          local current_file = vim.api.nvim_buf_get_name(0)

          pickers.new({}, {
            prompt_title = "LSP Document Symbols",
            finder = finders.new_table({
              results = items,
              entry_maker = function(s)
                return {
                  value = s,
                  ordinal = s.name,
                  display = function()
                    return displayer({
                      { s.pub and "●" or "○", s.pub and "DiagnosticOk" or "Comment" },
                      { s.kind:lower(),       "TelescopeResultsField" },
                      { s.name,               "TelescopeResultsConstant" },
                    })
                  end,
                  filename = current_file,
                  lnum = s.lnum,
                  col = s.col,
                }
              end,
            }),
            sorter = conf.generic_sorter({}),
            previewer = conf.qflist_previewer({}),
          }):find()
        end)
      end

      -- LSP implementations picker: demotes test-adjacent entries so
      -- the "real" implementation ends up preselected. Telescope preserves
      -- insertion order with an empty prompt, so we just pre-sort items
      -- (reals first → first input → bottom → preselected with the default
      -- descending strategy). Direct jump if only one result.
      local function smart_implementations()
        local clients = vim.lsp.get_clients({ bufnr = 0, method = "textDocument/implementation" })
        if #clients == 0 then return end
        local client = clients[1]
        local params = vim.lsp.util.make_position_params(0, client.offset_encoding)
        client:request("textDocument/implementation", params, function(err, result)
          if err or not result or vim.tbl_isempty(result) then return end
          local locs = vim.islist(result) and result or { result }

          if #locs == 1 then
            local first = locs[1]
            vim.lsp.util.show_document(
              { uri = first.uri or first.targetUri,
                range = first.targetSelectionRange or first.range or first.targetRange },
              client.offset_encoding, { focus = true })
            return
          end

          local items = vim.lsp.util.locations_to_items(locs, client.offset_encoding)
          table.sort(items, function(a, b)
            local fa = entry_is_low_priority(a.filename, a.text)
            local fb = entry_is_low_priority(b.filename, b.text)
            if fa ~= fb then return fa < fb end
            return (a.filename or "") < (b.filename or "")
          end)

          local pickers = require("telescope.pickers")
          local finders = require("telescope.finders")
          local conf = require("telescope.config").values
          local make_entry = require("telescope.make_entry")

          pickers.new({}, {
            prompt_title = "LSP Implementations",
            finder = finders.new_table({
              results = items,
              entry_maker = make_entry.gen_from_quickfix({}),
            }),
            sorter = conf.generic_sorter({}),
            previewer = conf.qflist_previewer({}),
          }):find()
        end)
      end

      vim.api.nvim_create_autocmd("LspAttach", {
        callback = function(ev)
          local map = function(k, fn, desc)
            vim.keymap.set("n", k, fn, { buffer = ev.buf, desc = desc })
          end
          map("<leader>a", vim.lsp.buf.code_action,                                       "code action")
          map("K",         vim.lsp.buf.hover,                                             "symbol info (type/docs)")
          map("<leader>K", vim.lsp.buf.hover,                                             "symbol info (type/docs)")
          map("<C-b>",     smart_goto,                                                    "goto def / list refs")
          map("gd",        smart_goto,                                                    "goto def / list refs")
          map("<C-S-b>",   smart_implementations,                                         "list implementations (real first)")
          map("<leader>e", vim.diagnostic.goto_next,                                      "next diagnostic")
          map("<leader>l", smart_list_symbols, "list symbols")
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
