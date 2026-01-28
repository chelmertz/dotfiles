local wezterm = require 'wezterm'
local config = wezterm.config_builder()

function get_appearance()
	-- if wezterm.gui then
	-- 	local a = wezterm.gui.get_appearance()
	-- 	print("a", a)
	-- 	return a
	-- end
	-- print("default Dark")
	-- return 'Dark'
	
	-- workaround from wezterm docs at https://wezfurlong.org/wezterm/config/lua/window/get_appearance.html
	-- but using 'color-scheme' rather than their mentioned 'gtk-theme'
	-- suitable for ubuntu 24.04 (or rather, gtk 4)
	local success, stdout = wezterm.run_child_process {
		'gsettings',
		'get',
		'org.gnome.desktop.interface',
		'color-scheme',
	}

	stdout = stdout:lower():gsub('%s+', '')
	local mapping = {
		['prefer-light'] = 'Light',
		['prefer-dark'] = 'Dark',
	}
	if mapping[stdout] then
		return mapping[stdout]
	end
	if stdout:find 'dark' then
		return 'Dark'
	end
	return 'Light'
end

function scheme_for_appearance(appearance)
	if appearance:find 'Dark' then
		-- return 'Batman' -- doesn't colorize green/red git index status in lazygit
		-- return 'Builtin Solarized Dark' -- has too many colors going on
		-- return '3024 (base16)' -- ok
		-- return 'Mono Amber (Gogh)' -- orange monochrome, doesn't work with lazygit
		-- return 'Mono Theme (terminal.sexy)' -- black and white mono (very mono, not even colors for git log or lazygit)
		-- return 'Night' -- OK, cyan and other colors, a bit noisy
		-- return 'Afterglow' -- grey and too little contrast
		-- return 'Atom' -- too purple
		-- return 'Blazer' -- too blue
		-- return 'BlulocoDark' -- gray + non contrast
		-- return 'Borland' -- insanely blue
		return 'Bright Lights' -- OK, many colors, monokai style, a little noisy
	else
		-- ...maybe the problem is with `fd`
		return 'Modus-Operandi-Tinted' -- light and nice, almost completely white but good colors in lazygit and acceptable in fd
		-- return 'Builtin Solarized Light' -- works OK mostly, not very big on contrasts
		-- return 'Gruvbox light, medium (base16)' -- has a very very light turquoise & green, hard to read against the white background
		-- return 'Gruvbox light, hard (base16)' -- has a very very light turquoise & green, hard to read against the white background
		-- return 'Gruvbox (Gogh)' -- has a very very light turquoise & green, hard to read against the white background
		-- return 'ayu_light' -- has a very very light turquoise & green, hard to read against the white background
		-- return 'CLRS' -- has a very very light turquoise & green, hard to read against the white background
		-- return 'Google (light) (terminal.sexy)' -- has good base colors in vim, fd output is too light still
	end
end

wezterm.on('update-status', function(window, pane)
	local overrides = window:get_config_overrides() or {}
	local current = get_appearance()
	local wanted = scheme_for_appearance(current)
	if overrides.color_scheme ~= wanted then
		overrides.color_scheme = wanted
		window:set_config_overrides(overrides)
	end
end)

config.color_scheme = scheme_for_appearance(get_appearance())
config.enable_tab_bar = false
config.scrollback_lines = 10000
config.enable_scroll_bar = true
config.font = wezterm.font("Go Mono", {weight="Regular", stretch="Normal", style="Normal"})
config.font_size = 15

return config
