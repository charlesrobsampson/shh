// Spike 04b: the same menu bar countdown as a JXA/AppKit status item (no CGo, no Go tray library).
ObjC.import('Cocoa');

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

const startSecs = parseInt($.NSProcessInfo.processInfo.environment.objectForKey('SPIKE_SECONDS').js || '120', 10);
const state = { remaining: startSecs, paused: false, level: 65, base: 30 };

function title() {
  if (state.paused) return `shh ⏸ ${state.level}%`;
  const r = state.remaining;
  const figureSpace = '\u2007';
  const left = (r < 60 ? `${r}s` : `${Math.round(r / 60)}m`).padStart(3, figureSpace);
  return `shh ${left} · ${state.level}%`;
}

const item = $.NSStatusBar.systemStatusBar.statusItemWithLength($.NSVariableStatusItemLength);
const button = item.button;
button.font = $.NSFont.monospacedDigitSystemFontOfSizeWeight(13, $.NSFontWeightRegular);
button.image = $.NSImage.imageWithSystemSymbolNameAccessibilityDescription('moon.fill', $());
button.image.template = true;
button.imagePosition = $.NSImageLeft;

const menu = $.NSMenu.alloc.init;
menu.autoenablesItems = false;
const entries = {};

function render() {
  button.title = $(title());
  entries.pause.title = $(state.paused ? 'Resume' : 'Pause');
  entries.status.title = $(state.remaining > 0 ? `focus ${state.level}%, ${state.remaining}s left` : `baseline ${state.base}%`);
}

ObjC.registerSubclass({
  name: 'ShhMenuTarget',
  superclass: 'NSObject',
  methods: {
    'tick:': { types: ['void', ['id']], implementation: function () {
      if (!state.paused && state.remaining > 0) state.remaining -= 1;
      if (state.remaining === 0) state.level = state.base;
      render();
    } },
    'pause:': { types: ['void', ['id']], implementation: function () { state.paused = !state.paused; console.log('click: pause, paused=' + state.paused); render(); } },
    'plus:': { types: ['void', ['id']], implementation: function () { state.remaining += 300; console.log('click: +5m'); render(); } },
    'minus:': { types: ['void', ['id']], implementation: function () { state.remaining = Math.max(0, state.remaining - 300); console.log('click: -5m'); render(); } },
    'stop:': { types: ['void', ['id']], implementation: function () { state.remaining = 0; state.level = state.base; console.log('click: stop'); render(); } },
    'quit:': { types: ['void', ['id']], implementation: function () { console.log('click: quit'); app.terminate($()); } },
  },
});
const target = $.ShhMenuTarget.alloc.init;

function add(key, text, action) {
  const mi = $.NSMenuItem.alloc.initWithTitleActionKeyEquivalent($(text), action || '', $(''));
  if (action) { mi.target = target; mi.enabled = true; }
  menu.addItem(mi);
  entries[key] = mi;
}
add('status', '', null);
entries.status.enabled = false;
menu.addItem($.NSMenuItem.separatorItem);
add('pause', 'Pause', 'pause:');
add('plus', '+5m', 'plus:');
add('minus', '-5m', 'minus:');
add('stop', 'Stop', 'stop:');
add('quit', 'Quit spike', 'quit:');
item.menu = menu;

render();
$.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(1, target, 'tick:', $(), true);
app.run;
