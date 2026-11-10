// The Todo button, or Shift+T (Gmail's "add to Tasks"), bound in tbkeys-lite
// to memsg:obsidian-todo@helmertz:todo (nix/thunderbird.nix), which arrives
// here as an external message. Each displayed message goes to the native host
// (obsidian_todo, nix/thunderbird.nix), which writes the task; a MailExtension
// cannot write files.
const TBKEYS = "tbkeys-lite@addons.thunderbird.net";

// "Ada L <ada@example.com>" -> "Ada L"; a bare address stays as it is.
const sender = (author) => (author || "").replace(/\s*<[^>]*>$/, "").replace(/^"(.*)"$/, "$1") || author;

function notify(title, message) {
  return messenger.notifications.create({ type: "basic", title, message });
}

// One notification per message: the outcome as the title, which message
// (sender: subject) as the body.
async function addTodos() {
  const [tab] = await messenger.tabs.query({ active: true, currentWindow: true });
  const shown = tab && (await messenger.messageDisplay.getDisplayedMessages(tab.id));
  // A MessageList since Thunderbird 121, a plain array before.
  const msgs = (shown && (shown.messages || shown)) || [];
  if (!msgs.length) return notify("todo failed", "no message open");
  for (const m of msgs) {
    const res = await messenger.runtime.sendNativeMessage("obsidian_todo", {
      id: m.headerMessageId,
      author: m.author,
      subject: m.subject,
    });
    console.log("obsidian-todo:", res);
    await notify(res.result, `${sender(m.author)}: ${m.subject || "(no subject)"}`);
  }
}

function todo(via) {
  console.log("obsidian-todo: via", via);
  addTodos().catch((e) => {
    console.error("obsidian-todo:", e);
    return notify("todo failed", String(e));
  });
}

messenger.runtime.onMessageExternal.addListener((message, sender) => {
  if (sender.id === TBKEYS && message === "todo") todo("Shift+T");
});
// The Todo button beside Claude review in the message header.
messenger.messageDisplayAction.onClicked.addListener(() => todo("button"));
