{ ... }:
{
  # WirePlumber suspends an idle audio node after session.suspend-timeout-seconds,
  # which defaults to 5. On this machine's Meteor Lake HD Audio codec the
  # power-down is audible: a short click a handful of seconds after any sound
  # ends. It reads exactly like a stray notification -- irregular, brief, and
  # present at night -- but it belongs to no application, which is why muting
  # tabs never helped. Pausing a video and counting to five reproduces it.
  #
  # 0 disables suspension for output nodes, keeping the codec powered. The cost
  # is a little idle power draw, which on a docked laptop is the right trade
  # against a click every time audio stops.
  xdg.configFile."wireplumber/wireplumber.conf.d/51-disable-suspend.conf".text = ''
    monitor.alsa.rules = [
      {
        matches = [
          { node.name = "~alsa_output.*" }
        ]
        actions = {
          update-props = {
            session.suspend-timeout-seconds = 0
          }
        }
      }
    ]
  '';
}
