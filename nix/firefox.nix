{ pkgs, ... }:
let
  # Helper to fetch Firefox addons from addons.mozilla.org by slug
  fetchAddon = { name, slug, addonId, sha256 }: pkgs.fetchFirefoxAddon {
    inherit name;
    url = "https://addons.mozilla.org/firefox/downloads/latest/${slug}/latest.xpi";
    inherit sha256;
    fixedExtensionID = addonId;
  };
in
{
  programs.firefox = {
    enable = true;
    profiles.default = {
      isDefault = true;
      # Extensions: to add more, find the addon slug from addons.mozilla.org URL
      # and the extension ID from about:debugging#/runtime/this-firefox
      # Then run: nix-prefetch-url <url> to get the sha256
      # For now, extensions will be managed manually via about:addons
      # until we populate sha256 hashes for each addon.
      #
      # Current extensions (from snap profile) that should be installed:
      # - uBlock Origin (uBlock0@raymondhill.net)
      # - Dark Reader (addon@darkreader.org)
      # - Bitwarden ({446900e4-71c2-419f-a6a7-df9c091e268b})
      # - 1Password ({d634138d-c276-4fc8-924b-40a0ea21d284})
      # - Vimium ({d7742d87-e61d-4b78-b8a1-b469842139fa})
      # - Facebook Container (@contain-facebook)
      # - Stylus ({7a7a4a92-a2a0-41d1-9fd7-1e92480d612d})
      # - Refined GitHub ({a4c4eda4-fb84-4a84-b4a1-f7c1cbf2a1ad})
      # - Consent-O-Matic (gdpr@cavi.au.dk)
      # - LanguageTool (languagetool-webextension@languagetool.org)
      # - Neat URL (neaturl@hugsmile.eu)
      # - Simple Translate (simple-translate@sienori)
      # - Grasp ({37e42980-a7c9-473c-96d5-13f18e0efc74})
      # - linkding injector ({19561335-5a63-4b4e-8182-1eced17f9b47})
      # - linkding extension ({61a05c39-ad45-4086-946f-32adb0a40a9d})
      # - Granted Containers ({b5e0e8de-ebfe-4306-9528-bcc18241a490})
      # - Medium-to-Scribe redirector ({acbdd727-8481-44e9-bb69-ed3c70876624})
      # - Ecosia ({d04b0b40-3dab-4f0b-97a6-04ec3eddbfb0})
      # - Github Whitespace Disabler (maksimovic@outlook.com)
      # - React DevTools (@react-devtools) [work profile]
      # - GNOME Shell integration (chrome-gnome-shell@gnome.org)
    };
  };
}
