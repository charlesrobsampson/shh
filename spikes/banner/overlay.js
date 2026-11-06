// Spike 03: overlay banner drawn with AppKit through JXA. Config arrives as JSON in OVERLAY_JSON.
ObjC.import('Cocoa');

const env = $.NSProcessInfo.processInfo.environment;
const cfg = Object.assign({
  title: 'shh', body: 'overlay test', color: '#ff9f0a', width: 360, height: 84,
  position: 'top-right', duration: 6, sound: 'none', bundle: '',
}, JSON.parse(env.objectForKey('OVERLAY_JSON').js || '{}'));

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

let outcome = 'expired';
function finish(result) {
  outcome = result;
  app.stop($());
  // stop: only takes effect after an event, so post one to wake the loop
  const wake = $.NSEvent.otherEventWithTypeLocationModifierFlagsTimestampWindowNumberContextSubtypeData1Data2(
    $.NSEventTypeApplicationDefined, $.NSMakePoint(0, 0), 0, 0, 0, $(), 0, 0, 0);
  app.postEventAtStart(wake, true);
}

ObjC.registerSubclass({
  name: 'ShhBannerBox',
  superclass: 'NSBox',
  methods: {
    'mouseDown:': { types: ['void', ['id']], implementation: function () { finish('clicked'); } },
    'acceptsFirstMouse:': { types: ['bool', ['id']], implementation: function () { return true; } },
  },
});
ObjC.registerSubclass({
  name: 'ShhBannerTimer',
  superclass: 'NSObject',
  methods: {
    'expire:': { types: ['void', ['id']], implementation: function () { finish('expired'); } },
  },
});

const hex = cfg.color.replace('#', '');
const channel = i => parseInt(hex.slice(i, i + 2), 16) / 255;
const color = $.NSColor.colorWithSRGBRedGreenBlueAlpha(channel(0), channel(2), channel(4), 1);

const area = $.NSScreen.mainScreen.visibleFrame;
const margin = 16;
const [v, h] = cfg.position.split('-');
const x = h === 'left' ? area.origin.x + margin
  : h === 'center' ? area.origin.x + (area.size.width - cfg.width) / 2
  : area.origin.x + area.size.width - cfg.width - margin;
const y = v === 'bottom' ? area.origin.y + margin
  : area.origin.y + area.size.height - cfg.height - margin;

const win = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSMakeRect(x, y, cfg.width, cfg.height), $.NSWindowStyleMaskBorderless, $.NSBackingStoreBuffered, false);
win.setLevel($.NSScreenSaverWindowLevel);
win.setOpaque(false);
win.setBackgroundColor($.NSColor.clearColor);
win.setHasShadow(true);
win.setCollectionBehavior($.NSWindowCollectionBehaviorCanJoinAllSpaces | $.NSWindowCollectionBehaviorFullScreenAuxiliary);

const view = $.ShhBannerBox.alloc.initWithFrame($.NSMakeRect(0, 0, cfg.width, cfg.height));
view.setBoxType($.NSBoxCustom);
view.setTitlePosition($.NSNoTitle);
view.setBorderWidth(0);
view.setCornerRadius(14);
view.setFillColor(color);
win.setContentView(view);

function label(text, size, bold, top) {
  const f = $.NSTextField.labelWithString($(text));
  f.setFont(bold ? $.NSFont.boldSystemFontOfSize(size) : $.NSFont.systemFontOfSize(size));
  f.setTextColor($.NSColor.whiteColor);
  f.setFrame($.NSMakeRect(16, top, cfg.width - 32, size + 6));
  view.addSubview(f);
}
label(cfg.title, 15, true, cfg.height - 34);
label(cfg.body, 13, false, cfg.height - 58);

win.orderFront($());
if (cfg.sound !== 'none') $.NSSound.soundNamed($(cfg.sound)).play;
if (cfg.duration > 0) {
  $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(
    cfg.duration, $.ShhBannerTimer.alloc.init, 'expire:', $(), false);
}

app.run;
win.orderOut($());
console.log(outcome);
if (outcome === 'clicked' && cfg.bundle) {
  const opened = $.NSTask.launchedTaskWithLaunchPathArguments('/usr/bin/open', ['-b', cfg.bundle]);
}
