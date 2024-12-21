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
		return 'Builtin Solarized Dark'
	else
		print("schema  Light")
		return 'Builtin Solarized Light'
	end
end

config.color_scheme = scheme_for_appearance(get_appearance())
config.enable_tab_bar = false

return config
