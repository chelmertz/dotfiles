// ==UserScript==
// @name         GitHub → prr:// review link
// @namespace    chelmertz/dotfiles
// @version      1.0
// @description  Adds a "prr review" link to github PR pages that opens the prr:// scheme handler (bin/prr-open), launching `pr <owner>/<repo>/<number>` in ghostty.
// @match        https://github.com/*/*/pull/*
// @run-at       document-idle
// @grant        none
// ==/UserScript==

// Install in a userscript manager (Violentmonkey / Tampermonkey). Refined
// GitHub can't inject custom links, hence a userscript.
//
// The prr:// scheme is registered to ~/bin/prr-open via:
//   xdg-mime default prr-open.desktop x-scheme-handler/prr
// (see prr-clickable-links.md).

(function () {
  "use strict";

  // Path looks like /<owner>/<repo>/pull/<number>[/...]. Bail on anything else
  // (the @match already narrows, but tabs like /pull/N/files still qualify).
  const m = location.pathname.match(
    /^\/([^/]+)\/([^/]+)\/pull\/(\d+)/
  );
  if (!m) return;

  const [, owner, repo, number] = m;
  const href = `prr://${owner}/${repo}/${number}`;

  function inject() {
    if (document.getElementById("prr-review-link")) return;

    // Anchor next to the PR title header actions if present, else fixed corner.
    const link = document.createElement("a");
    link.id = "prr-review-link";
    link.href = href;
    link.textContent = "⌁ prr review";
    link.title = `Open ${owner}/${repo}/${number} in prr (ghostty)`;
    link.style.cssText = [
      "position:fixed",
      "bottom:1rem",
      "right:1rem",
      "z-index:9999",
      "padding:0.4rem 0.7rem",
      "background:#1f6feb",
      "color:#fff",
      "border-radius:6px",
      "font:600 12px/1 system-ui,sans-serif",
      "text-decoration:none",
      "box-shadow:0 1px 4px rgba(0,0,0,.4)",
    ].join(";");
    document.body.appendChild(link);
  }

  inject();
  // GitHub is an SPA (Turbo/pjax navigations) — re-inject on soft nav.
  document.addEventListener("turbo:load", inject);
  document.addEventListener("pjax:end", inject);
})();
