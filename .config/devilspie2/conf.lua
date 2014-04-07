--workspaces:
--1 Chrome
--2 Code
--3 Code
--4 Spotify
--5 Chat

-- help: https://github.com/gusnan/devilspie2

--debug_print("Window Name: " .. get_window_name());
--debug_print("Application name: " .. get_application_name())

local app_name = get_application_name()

if (app_name == "Pidgin") then
	set_window_workspace(5)
end

if (app_name == "spotify" or app_name == "Spotify Premium - Linux Preview") then
	set_window_workspace(4)
end
