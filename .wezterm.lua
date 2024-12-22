local wezterm = require 'wezterm'
local config = wezterm.config_builder()

function get_appearance()
	if wezterm.gui then
		local a = wezterm.gui.get_appearance()
		print("a", a)
		return a
	end
	print("default Dark")
	return 'Dark'
end

function scheme_for_appearance(appearance)
	if appearance:find 'Dark' then
		print("schema  Dark")
		-- 'Batman' doesn't colorize green/red git index status in lazygit
		-- 'Builtin Solarized Dark' has too many colors going on
		return '3024 (base16)'
	else
		print("schema  Light")
		return 'Builtin Solarized Light'
	end
end

config.color_scheme = scheme_for_appearance(get_appearance())
config.enable_tab_bar = false

return config
