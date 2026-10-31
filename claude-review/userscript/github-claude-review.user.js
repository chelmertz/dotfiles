// ==UserScript==
// @name         GitHub → claude-review
// @namespace    chelmertz/dotfiles
// @version      1.0
// @description  Adds a "claude review" button to GitHub PR pages that starts, or resumes, the Claude Code review session for that PR on your machine via the claude-review CLI.
// @match        https://github.com/*
// @run-at       document-idle
// @grant        GM_setClipboard
// ==/UserScript==

// Install in Violentmonkey or Tampermonkey. See ../README.md for the CLI
// and the per-OS scheme handler this talks to.
//
// Two actions, both ending in the same CLI call:
//   "claude review"  navigates to claude-review://owner/repo/number, which a
//                    registered scheme handler forwards to the CLI.
//   "copy cmd"       puts `claude-review <pr url>` on the clipboard, for a
//                    terminal. Works everywhere, no handler needed.
//
// Which buttons show depends on the OS the browser reports: Linux and macOS
// get both, since a handler can be set up there; anything else gets only
// "copy cmd". The CLI itself runs on Linux and macOS. Edit HANDLER_PLATFORMS
// if that is wrong for you.

(function () {
	"use strict";

	const HANDLER_PLATFORMS = /linux|mac/i;

	const parsePR = (path) => {
		const m = path.match(/^\/([A-Za-z0-9._-]+)\/([A-Za-z0-9._-]+)\/pull\/(\d+)/);
		return m ? { owner: m[1], repo: m[2], number: m[3] } : null;
	};

	const platform = () =>
		(navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";

	const hasHandler = () => HANDLER_PLATFORMS.test(platform());

	const copyText = (text) => {
		if (typeof GM_setClipboard === "function") {
			GM_setClipboard(text);
			return Promise.resolve();
		}
		return navigator.clipboard.writeText(text);
	};

	const button = (label, title) => {
		const a = document.createElement("a");
		a.href = "#";
		a.textContent = label;
		a.title = title;
		a.style.cssText = [
			"display:inline-block",
			"padding:0.4rem 0.7rem",
			"background:#1f6feb",
			"color:#fff",
			"border-radius:6px",
			"font:600 12px/1 system-ui,sans-serif",
			"text-decoration:none",
			"box-shadow:0 1px 4px rgba(0,0,0,.4)",
			"cursor:pointer",
		].join(";");
		return a;
	};

	const inject = () => {
		if (document.getElementById("claude-review-buttons")) return;
		const pr = parsePR(location.pathname);
		if (!pr) return;

		const url = `https://github.com/${pr.owner}/${pr.repo}/pull/${pr.number}`;
		const scheme = `claude-review://${pr.owner}/${pr.repo}/${pr.number}`;
		const cmd = `claude-review ${url}`;

		const box = document.createElement("div");
		box.id = "claude-review-buttons";
		box.style.cssText = "position:fixed;bottom:1rem;right:1rem;z-index:9999;display:flex;gap:0.4rem";

		const open = button("claude review", `Open ${scheme} (needs the scheme handler)`);
		open.addEventListener("click", (e) => {
			e.preventDefault();
			location.href = scheme;
		});

		const copy = button("copy cmd", `Copy "${cmd}" for a terminal`);
		copy.style.background = "#30363d";
		copy.addEventListener("click", (e) => {
			e.preventDefault();
			copyText(cmd).then(
				() => { copy.textContent = "copied"; setTimeout(() => { copy.textContent = "copy cmd"; }, 1500); },
				() => { copy.textContent = "copy failed"; },
			);
		});

		if (hasHandler()) {
			box.append(open, copy);
		} else {
			box.append(copy);
		}
		document.body.appendChild(box);
	};

	// Matched on all of github.com because GitHub navigates with Turbo: a
	// script matched only on /pull/ URLs never loads when the PR is reached
	// by clicking from a list. inject() returns early on non-PR pages.
	inject();
	// Re-inject on soft navigation.
	document.addEventListener("turbo:load", () => {
		const old = document.getElementById("claude-review-buttons");
		if (old) old.remove();
		inject();
	});
})();
