// Draws one alert banner in a window of its own, so Notification Center and the banner suppressor never touch it.
// The banner is described by JSON in SHH_BANNER. On exit the last line printed is "clicked" or "expired"; the caller opens the
// source app after a click. Prototyped in spikes/banner/overlay.js (docs/spikes/03-banner.md).
ObjC.import('Cocoa');

const cfg = JSON.parse($.NSProcessInfo.processInfo.environment.objectForKey('SHH_BANNER').js);

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

let outcome = 'expired';
function finish(result) {
  outcome = result;
  app.stop($());
  // stop only takes effect after an event, so post one to wake the run loop
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
const gap = 10;
const [vertical, horizontal] = cfg.position.split('-');
const x = horizontal === 'left' ? area.origin.x + margin
  : horizontal === 'center' ? area.origin.x + (area.size.width - cfg.width) / 2
  : area.origin.x + area.size.width - cfg.width - margin;
const step = cfg.slot * (cfg.height + gap); // banners shown together stack away from the screen edge
const y = vertical === 'bottom' ? area.origin.y + margin + step
  : area.origin.y + area.size.height - cfg.height - margin - step;

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

function label(text, size, bold, frame, lines) {
  const field = $.NSTextField.labelWithString($(text));
  field.setFont(bold ? $.NSFont.boldSystemFontOfSize(size) : $.NSFont.systemFontOfSize(size));
  field.setTextColor($.NSColor.whiteColor);
  field.setLineBreakMode($.NSLineBreakByTruncatingTail);
  field.setMaximumNumberOfLines(lines);
  field.setFrame(frame);
  view.addSubview(field);
}
// The title is one line at the top; the body takes as many lines as the rest of the banner holds.
label(cfg.title, 15, true, $.NSMakeRect(16, cfg.height - 32, cfg.width - 32, 20), 1);
const bodyHeight = cfg.height - 42;
label(cfg.body, 13, false, $.NSMakeRect(16, 8, cfg.width - 32, bodyHeight), Math.max(1, Math.floor(bodyHeight / 16)));

win.orderFront($());
if (cfg.sound !== 'none') $.NSSound.soundNamed($(cfg.sound)).play;
if (cfg.duration > 0) {
  $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(
    cfg.duration, $.ShhBannerTimer.alloc.init, 'expire:', $(), false);
}

app.run;
win.orderOut($());
console.log(outcome);
