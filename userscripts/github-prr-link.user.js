// ==UserScript==
// @name         GitHub → prr:// review link
// @namespace    chelmertz/dotfiles
// @version      1.0
// @description  Adds a "prr review" link to github PR pages that opens the prr:// scheme handler (bin/prr-open), launching `pr <owner>/<repo>/<number>` in ghostty.
// @match        https://github.com/*/*/pull/*
// @match        http://localhost:9876/*
// @run-at       document-idle
// @grant        none
// ==/UserScript==

// Install in a userscript manager (Violentmonkey / Tampermonkey).
//
// The prr:// scheme is registered to ~/bin/prr-open via:
//   xdg-mime default prr-open.desktop x-scheme-handler/prr

(function () {
	"use strict";

	const prrLink = (ghPrUrl) => {
		console.log("url", ghPrUrl);
		const m = ghPrUrl.match(
			/\/([^/]+)\/([^/]+)\/pull\/(\d+)/
		);
		console.log("m", m);
		if (!m) return;

		const [, owner, repo, number] = m;

		const href = `prr://${owner}/${repo}/${number}`;
		const a = document.createElement("a");
		a.href = href;
		a.textContent = "prr review";
		a.title = `Open ${owner}/${repo}/${number} in prr (ghostty)`;

		return a;
	};

	const github = () => {
		const injectGithub = () => {
			if (document.body.classList.contains("prr-injected")) return;

			const link = prrLink(location.pathname);
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
			document.body.classList.add("prr-injected");
		};

		injectGithub();
		// GitHub is an SPA (Turbo/pjax navigations) — re-inject on soft nav.
		document.addEventListener("turbo:load", injectGithub);
		document.addEventListener("pjax:end", injectGithub);
	};

	const elly = () => {
		if (document.body.classList.contains("prr-injected")) return;

		document.querySelectorAll("a.pr-title").forEach(el => {
			const link = prrLink(el.href);
			link.classList.add("inline");
			link.classList.add("rounded");
			link.classList.add("action");
			link.style.cssText = [
				""
			].join(";");
			el.closest(".pr").querySelector(".action:last-of-type").after(link);
		});

		document.body.classList.add("prr-injected");
	};

	switch (location.host) {
		case "github.com":
			github();
			break;
		case "localhost:9876":
			elly();
			break;
	}
})();
