{ ... }:
{
  services.espanso = {
    enable = true;

    configs.default = {
      search_shortcut = "CTRL+CMD+ALT+SPACE";
    };

    matches.default.matches = [
      { trigger = "::espanso"; replace = "Hi there!"; }
      {
        trigger = "::date";
        replace = "{{mydate}}";
        vars = [
          {
            name = "mydate";
            type = "date";
            params.format = "%Y-%m-%d";
          }
        ];
      }
      {
        trigger = "::today";
        replace = "{{today}}";
        vars = [
          {
            name = "today";
            type = "date";
            params.format = "%Y-%m-%d";
          }
        ];
      }
      { trigger = "::shell"; replace = "```shell\n$|$\n```"; }
      { trigger = "::pov"; replace = "point of view"; }
      { trigger = "::ttm"; replace = "time to market (TTM)"; }
      { trigger = "::bc"; replace = "backwards-compatible"; }
      { trigger = "::Bc"; replace = "Backwards-compatible"; }
      { trigger = "::lgtm"; replace = "looks good to me"; }
      { trigger = "::Lgtm"; replace = "Looks good to me"; }
      { trigger = "::wdyt"; replace = "what do you think?"; }
      { trigger = "::Wdyt"; replace = "What do you think?"; }
      { trigger = "::wrt"; replace = "with regard to"; }
      { trigger = "::Wrt"; replace = "With regard to"; }
      { trigger = "::iirc"; replace = "if I recall correctly"; }
      { trigger = "::Iirc"; replace = "If I recall correctly"; }
      { trigger = "::lmk"; replace = "let me know"; }
      { trigger = "::Lmk"; replace = "Let me know"; }
      { trigger = "::afaik"; replace = "as far as I know"; }
      { trigger = "::Afaik"; replace = "As far as I know"; }
      { trigger = "::RO"; replace = "read-only"; }
      { trigger = "::RW"; replace = "read-write"; }
      { trigger = "::AC"; replace = "acceptance criteria"; }
      { trigger = "::Ac"; replace = "Acceptance criteria"; }
      { trigger = "::afaict"; replace = "as far as I can tell"; }
      { trigger = "::Afaict"; replace = "As far as I can tell"; }
      { trigger = "::imo"; replace = "in my opinion"; }
      { trigger = "::Imo"; replace = "In my opinion"; }
      { trigger = "::otoh"; replace = "on the other hand"; }
      { trigger = "::Otoh"; replace = "On the other hand"; }

      # icons (https://www.compart.com/en/unicode)
      { trigger = "::plus"; replace = "➕"; }
      { trigger = "::thumb"; replace = "👍"; }
      { trigger = "::raised"; replace = "🙌"; }
      { trigger = "::pray"; replace = "🙏"; }
      { trigger = "::joy"; replace = "😂"; }
      { trigger = "::sweat"; replace = "😅"; }
      { trigger = "::eye"; replace = "👀"; }
      { trigger = "::see"; replace = "🙈"; }
      { trigger = "::woozy"; replace = "🥴"; }
      { trigger = "::tada"; replace = "🎉"; }
      { trigger = "<->"; replace = "↔"; }
      { trigger = "::PR"; replace = "pull request"; }
      { trigger = "::x"; replace = "×"; }
      { trigger = "::check"; replace = "✅"; }
      { trigger = "::sad"; replace = "☹"; }
      { trigger = "::sml"; replace = "😊"; }
      { trigger = "::strong"; replace = "💪"; }
      { trigger = "::ba"; replace = "😎"; }
      { trigger = "::happy"; replace = "😄"; }
      { trigger = "::cry"; replace = "😭"; }
      { trigger = "::wow"; replace = "😮"; }

      # for https://github.com/akavel/up
      { trigger = "::up"; replace = "|& up"; }

      {
        trigger = "::mermseq";
        replace = "sequenceDiagram\n\nparticipant client\n\nparticipant api\n\nclient -->>+ api: hello\n\napi -->>- client: good bye\n";
      }
    ];
  };
}
