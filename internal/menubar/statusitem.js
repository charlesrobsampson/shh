// The menu bar item: an icon with text next to it and a menu, drawn with AppKit through JXA (docs/spikes/04-menubar.md).
// Go sends the item's content as JSON lines on stdin, {"title": "...", "items": [{"id", "title", "separator"}]}, and the script
// prints "event click <id>" when a menu item with an id is chosen. SHH_MENUBAR is {"parent": <pid>}: the script quits when its
// parent is no longer that process, and stops writing then too (a write to a closed pipe would kill it).
ObjC.import('Cocoa');
ObjC.import('unistd');

const cfg = JSON.parse($.NSProcessInfo.processInfo.environment.objectForKey('SHH_MENUBAR').js);
const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

function attached() { return $.getppid() === cfg.parent; }
function emit(line) { if (attached()) console.log('event ' + line); }

const item = $.NSStatusBar.systemStatusBar.statusItemWithLength($.NSVariableStatusItemLength);
const button = item.button;
// Digits all have the same width, so the title does not jitter as the seconds tick down.
button.font = $.NSFont.monospacedDigitSystemFontOfSizeWeight(13, $.NSFontWeightRegular);
button.image = $.NSImage.imageWithSystemSymbolNameAccessibilityDescription('moon.fill', $());
button.image.template = true;
button.imagePosition = $.NSImageLeft;
button.title = $('shh');

const menu = $.NSMenu.alloc.init;
menu.autoenablesItems = false;
item.menu = menu;

let current = [];

ObjC.registerSubclass({
  name: 'ShhMenuTarget',
  superclass: 'NSObject',
  methods: {
    // The selector is a plain string and each item is enabled by hand: otherwise macOS greys the items out (spike 04).
    'click:': { types: ['void', ['id']], implementation: function (sender) {
      const entry = current[sender.tag];
      if (entry && entry.id) emit('click ' + entry.id);
    } },
    'watch:': { types: ['void', ['id']], implementation: function () { if (!attached()) app.terminate($()); } },
  },
});
const target = $.ShhMenuTarget.alloc.init;

function render(view) {
  button.title = $(view.title);
  menu.removeAllItems;
  current = view.items;
  view.items.forEach((entry, i) => {
    if (entry.separator) { menu.addItem($.NSMenuItem.separatorItem); return; }
    const clickable = !!entry.id;
    const mi = $.NSMenuItem.alloc.initWithTitleActionKeyEquivalent($(entry.title), clickable ? 'click:' : '', $(''));
    mi.tag = i;
    if (clickable) { mi.target = target; mi.enabled = true; } else { mi.enabled = false; }
    menu.addItem(mi);
  });
}

const stdin = $.NSFileHandle.fileHandleWithStandardInput;
let pending = '';
ObjC.registerSubclass({
  name: 'ShhMenuInput',
  superclass: 'NSObject',
  methods: {
    'ready:': { types: ['void', ['id']], implementation: function () {
      const data = stdin.availableData;
      if (data.length === 0) { app.terminate($()); return; } // Go closed our input: it is done with us
      pending += $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding).js;
      let end;
      while ((end = pending.indexOf('\n')) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        try { render(JSON.parse(line)); } catch (e) { /* ignore a malformed line */ }
      }
      stdin.waitForDataInBackgroundAndNotify;
    } },
  },
});
const input = $.ShhMenuInput.alloc.init;
$.NSNotificationCenter.defaultCenter.addObserverSelectorNameObject(input, 'ready:', $.NSFileHandleDataAvailableNotification, stdin);
stdin.waitForDataInBackgroundAndNotify;

$.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(1, target, 'watch:', $(), true);
app.run;
