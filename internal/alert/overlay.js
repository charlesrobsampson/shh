// Draws one alert banner in a window of its own, so Notification Center and the banner suppressor never touch it.
// The banner is described by JSON in SHH_BANNER. On exit the last line printed is "opened" (the Go to button), "dismissed" or
// "expired", and for "opened" the source app (cfg.bundle) is opened from here. Prototyped in spikes/banner/overlay.js (docs/spikes/03-banner.md).
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
    // A click on the background. Buttons take their own clicks, so this is only reached outside them.
    'mouseDown:': { types: ['void', ['id']], implementation: function () { if (cfg.dismissOnClick) finish('dismissed'); } },
    'acceptsFirstMouse:': { types: ['bool', ['id']], implementation: function () { return true; } },
  },
});
ObjC.registerSubclass({
  name: 'ShhBannerButton',
  superclass: 'NSButton',
  methods: {
    'acceptsFirstMouse:': { types: ['bool', ['id']], implementation: function () { return true; } },
  },
});
ObjC.registerSubclass({
  name: 'ShhBannerActions',
  superclass: 'NSObject',
  methods: {
    'press:': { types: ['void', ['id']], implementation: function (sender) { press(cfg.buttons[sender.tag]); } },
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
  // Wrapping labels are selectable by default, and a selectable field keeps the mouse for text selection, so a click on the text
  // would never reach the banner. Static text lets the click through.
  field.setSelectable(false);
  // A truncating line-break mode would turn the field into a single-line one (measured: wraps = false). To wrap and still end
  // in an ellipsis when the text runs past the last allowed line, use word wrapping plus truncatesLastVisibleLine.
  field.setLineBreakMode($.NSLineBreakByWordWrapping);
  field.cell.setTruncatesLastVisibleLine(true);
  field.setMaximumNumberOfLines(lines);
  field.setFrame(frame);
  view.addSubview(field);
  return field;
}

// Layout, in points, matching the Go side: 12 of padding, a 20 point title line, a 4 point gap, 16 points per body line, and when
// there are buttons a 32 point row at the bottom. The caller sized the banner to its text; text beyond what fits ends in an ellipsis.
const buttonRow = cfg.buttons.length > 0 ? 32 : 0;
let height = cfg.height;
const title = label(cfg.title, 15, true, $.NSMakeRect(16, height - 12 - 20, cfg.width - 32, 20), 1);
const body = label(cfg.body, 13, false, $.NSMakeRect(16, 12 + buttonRow, cfg.width - 32, height - 12 - 20 - 4 - 12 - buttonRow), 1);
function layoutText() {
  const room = height - 12 - 20 - 4 - 12 - buttonRow;
  title.setFrame($.NSMakeRect(16, height - 12 - 20, cfg.width - 32, 20));
  body.setFrame($.NSMakeRect(16, 12 + buttonRow, cfg.width - 32, room));
  body.setMaximumNumberOfLines(Math.max(1, Math.floor(room / 16)));
}
layoutText();

const actions = $.ShhBannerActions.alloc.init;
const buttons = cfg.buttons.map((spec, i) => {
  const button = $.ShhBannerButton.alloc.init;
  button.setTitle($(spec.label));
  button.setBezelStyle($.NSBezelStyleRounded);
  button.setControlSize($.NSControlSizeSmall);
  button.setFont($.NSFont.systemFontOfSize(12));
  button.setTag(i);
  button.setTarget(actions);
  button.setAction('press:');
  view.addSubview(button);
  return button;
});
// Visible buttons are laid out left to right in the configured order, evenly sharing the width.
function layoutButtons() {
  const shown = buttons.filter(b => !b.isHidden);
  const gap = 8;
  const each = Math.min(96, (cfg.width - 32 - gap * (shown.length - 1)) / Math.max(shown.length, 1));
  shown.forEach((b, n) => b.setFrame($.NSMakeRect(16 + n * (each + gap), 8, each, 24)));
}
layoutButtons();

let timer = null;
// Grows the banner in place to show more of the text. A banner at the top keeps its top edge, one at the bottom its bottom edge.
function expand() {
  const grown = Math.max(cfg.previewHeight, height);
  const frame = win.frame;
  const bottomUp = vertical === 'bottom';
  const top = frame.origin.y + frame.size.height;
  const newY = bottomUp ? frame.origin.y : Math.max(top - grown, area.origin.y + margin);
  height = grown;
  win.setFrameDisplay($.NSMakeRect(frame.origin.x, newY, cfg.width, grown), true);
  view.setFrame($.NSMakeRect(0, 0, cfg.width, grown));
  layoutText();
}
function press(spec) {
  if (spec.type === 'dismiss') finish('dismissed');
  else if (spec.type === 'goto') finish('opened');
  else if (spec.type === 'preview') {
    // Someone reading the full text should not have it vanish: stop the auto-dismiss timer.
    if (timer) { timer.invalidate; timer = null; }
    expand();
    buttons[cfg.buttons.indexOf(spec)].setHidden(true);
    layoutButtons();
  }
}

win.orderFront($());
if (cfg.sound !== 'none') $.NSSound.soundNamed($(cfg.sound)).play;
if (cfg.duration > 0) {
  timer = $.NSTimer.scheduledTimerWithTimeIntervalTargetSelectorUserInfoRepeats(
    cfg.duration, $.ShhBannerTimer.alloc.init, 'expire:', $(), false);
}

app.run;
win.orderOut($());
if (outcome === 'opened' && cfg.bundle) {
  // Opened from here, not by the process that started us, which may already have exited. This must come before the console.log
  // below: if that process is gone, its pipe is closed and the write kills this script (SIGPIPE) before it could do anything else.
  $.NSTask.launchedTaskWithLaunchPathArguments('/usr/bin/open', ['-b', cfg.bundle]);
}
console.log(outcome);
