{ pkgs, ... }:
{
  # Replaces vlc — covers all vlc use cases + more with uosc (modern OSD/UI,
  # frame stepping, speed control). GIF inspection was the trigger.
  programs.mpv = {
    enable = true;
    config = {
      osc = false; # disable built-in OSC so uosc can take over
      osd-bar = false; # uosc provides its own bar
    };
    scripts = with pkgs.mpvScripts; [
      uosc # modern OSD/UI
      thumbfast # seekbar thumbnail previews (uosc integrates with this)
      autoload # auto-loads other files in same directory as playlist
    ];
  };

  # Auto-convert GIFs to mp4 so thumbfast/seeking works. Cached by md5 in /tmp.
  xdg.configFile."mpv/scripts/gif-convert.lua".text = ''
    local utils = require("mp.utils")

    local function is_gif(path)
      return path:lower():match("%.gif$") or path:lower():match("%.gif[?#]")
    end

    local function hash_string(s)
      local r = mp.command_native({
        name = "subprocess",
        args = {"md5sum"},
        stdin_data = s,
        capture_stdout = true,
      })
      return r and r.stdout and r.stdout:match("^%S+")
    end

    mp.add_hook("on_load", 50, function()
      local path = mp.get_property("stream-open-filename")
      if not path or not is_gif(path) then return end

      local hash = hash_string(path)
      if not hash then return end

      local tmp = "/tmp/mpv-gif-" .. hash .. ".mp4"

      if not utils.file_info(tmp) then
        local r = mp.command_native({
          name = "subprocess",
          args = {"ffmpeg", "-y", "-loglevel", "error",
                  "-i", path,
                  "-c:v", "libx264", "-crf", "0", "-pix_fmt", "yuv444p", "-g", "1",
                  tmp},
        })
        if not r or r.status ~= 0 then return end
      end

      mp.set_property("stream-open-filename", tmp)
      mp.set_property("loop-file", "inf")
    end)
  '';

  # ff2mpv: right-click "Open in mpv" in Firefox (needs ff2mpv extension installed)
  home.file.".mozilla/native-messaging-hosts/ff2mpv.json".text = builtins.toJSON {
    name = "ff2mpv";
    description = "ff2mpv's native messaging host";
    path = "${pkgs.ff2mpv-go}/bin/ff2mpv-go";
    type = "stdio";
    allowed_extensions = [ "ff2mpv@yossarian.net" ];
  };
}
