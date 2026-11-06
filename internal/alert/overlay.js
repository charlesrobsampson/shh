// Draws one alert banner in a window of its own, so Notification Center and the banner suppressor never touch it.
// The banner is described by JSON in SHH_BANNER. On exit the last line printed is "clicked" or "expired", and after a click the
// source app (cfg.bundle), if there is one, is opened from here. Prototyped in spikes/banner/overlay.js (docs/spikes/03-banner.md).
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
const [vertical, horizontal] = cfg.position.split('-');
const x = horizontal === 'left' ? area.origin.x + margin
  : horizontal === 'center' ? area.origin.x + (area.size.width - cfg.width) / 2
  : area.origin.x + area.size.width - cfg.width - margin;
// cfg.offset keeps banners shown together from overlapping; stay on screen if there are very many.
const lowest = area.origin.y + margin;
const y = vertical === 'bottom' ? Math.min(lowest + cfg.offset, area.origin.y + area.size.height - cfg.height - margin)
  : Math.max(area.origin.y + area.size.height - cfg.height - margin - cfg.offset, lowest);

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
  // wrappingLabelWithString wraps long text onto further lines; labelWithString would keep it on one line and clip it.
  const field = $.NSTextField.wrappingLabelWithString($(text));
  field.setFont(bold ? $.NSFont.boldSystemFontOfSize(size) : $.NSFont.systemFontOfSize(size));
  field.setTextColor($.NSColor.whiteColor);
  if (lines > 1) {
    // A truncating line-break mode would turn the field into a single-line one (measured: wraps = false). To wrap and still end
    // in an ellipsis when the text runs past the last allowed line, use word wrapping plus truncatesLastVisibleLine.
    field.setLineBreakMode($.NSLineBreakByWordWrapping);
    field.cell.setTruncatesLastVisibleLine(true);
  } else {
    field.setLineBreakMode($.NSLineBreakByTruncatingTail);
  }
  field.setMaximumNumberOfLines(lines);
  field.setFrame(frame);
  view.addSubview(field);
}
// Layout, in points, matching the Go side: 12 of padding, a 20 point title line, a 4 point gap, then 16 points per body line.
// The caller sized the banner to its text; text beyond what fits ends in an ellipsis.
const bodyHeight = cfg.height - 12 - 20 - 4 - 12;
label(cfg.title, 15, true, $.NSMakeRect(16, cfg.height - 12 - 20, cfg.width - 32, 20), 1);
label(cfg.body, 13, false, $.NSMakeRect(16, 12, cfg.width - 32, bodyHeight), Math.max(1, Math.floor(bodyHeight / 16)));

win.orderFront($());
if (cfg.sound !== 'none') $.NSSound.soundNamed($(cfg.sound)).play;
if (cfg.duration > 0) {
  $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(
    cfg.duration, $.ShhBannerTimer.alloc.init, 'expire:', $(), false);
}

app.run;
win.orderOut($());
if (outcome === 'clicked' && cfg.bundle) {
  // Opened from here, not by the process that started us, which may already have exited. This must come before the console.log
  // below: if that process is gone, its pipe is closed and the write kills this script (SIGPIPE) before it could do anything else.
  $.NSTask.launchedTaskWithLaunchPathArguments('/usr/bin/open', ['-b', cfg.bundle]);
}
console.log(outcome);
