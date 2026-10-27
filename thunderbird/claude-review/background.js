// GitHub PR notification Message-IDs are owner/repo/pull/123@github.com, or
// owner/repo/pull/123/c456@github.com for comments and reviews, so the PR
// comes from the header without parsing the body.
const PR = /^([^/]+)\/([^/]+)\/pull\/(\d+)[/@]/;

function reviewUrl(msg) {
  const m = msg && PR.exec(msg.headerMessageId);
  return m && `claude-review://${m[1]}/${m[2]}/${m[3]}`;
}

// A MailExtension cannot start a process, so the native host (claude_review,
// declared in nix/thunderbird.nix) hands the URL to xdg-open.
messenger.messageDisplayAction.onClicked.addListener(async (tab) => {
  try {
    const url = reviewUrl(await messenger.messageDisplay.getDisplayedMessage(tab.id));
    if (!url) return;
    const res = await messenger.runtime.sendNativeMessage("claude_review", { url });
    console.log("claude-review:", url, res);
  } catch (e) {
    console.error("claude-review failed:", e);
  }
});
