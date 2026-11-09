// Draws one alert banner in a window of its own, so Notification Center and the banner suppressor never touch it.
// The banner is described by JSON in SHH_BANNER and, while Go is attached, moved by JSON lines on stdin. On exit the last line printed is "opened" (the Go to button), "dismissed" or
// "expired", and for "opened" the source app (cfg.bundle) is opened from here. Prototyped in spikes/banner/overlay.js (docs/spikes/03-banner.md).
ObjC.import('Cocoa');
ObjC.import('unistd');

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
const baseX = horizontal === 'left' ? area.origin.x + margin
  : horizontal === 'center' ? area.origin.x + (area.size.width - cfg.width) / 2
  : area.origin.x + area.size.width - cfg.width - margin;
// Where the window goes. The banners at one position form a stack and Go says how far down it each one sits (offset) and, when the
// stack is too tall for the screen and continues in another column, how far in from the screen edge (indent). A top banner keeps its
// top edge when it grows, a bottom one its bottom edge.
let indent = cfg.indent;
let offset = cfg.offset;
let height = cfg.height;
function frameFor(ind, off, h) {
  // Further columns go towards the middle of the screen; a centred stack grows to the right.
  const x = horizontal === 'left' || horizontal === 'center' ? baseX + ind : baseX - ind;
  const lowest = area.origin.y + margin;
  const y = vertical === 'bottom' ? Math.min(lowest + off, area.origin.y + area.size.height - h - margin)
    : Math.max(area.origin.y + area.size.height - h - margin - off, lowest);
  return $.NSMakeRect(x, y, cfg.width, h);
}

const win = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  frameFor(indent, offset, height), $.NSWindowStyleMaskBorderless, $.NSBackingStoreBuffered, false);
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
// Anything the script tells Go is a line starting "event ". When Go is gone nothing is written any more, because a write to a closed
// pipe kills the script (SIGPIPE). Go is gone when our parent is no longer the process that started us, which is checked right before every write: the
// notification for the end of stdin does not arrive reliably, and a timer would leave a window.
// The parent's id comes from Go (cfg.parent), not from asking now: a command like `shh alert test` can exit before this script has
// started, and then "my parent" would already be launchd and the check could never fail.
function attached() { return cfg.managed && $.getppid() === cfg.parent; }
function emit(line) { if (attached()) console.log('event ' + line); }

// Moves and resizes the window in place, with the banner's contents following it.
function layoutTo(newIndent, newOffset, newHeight) {
  if (newIndent === indent && newOffset === offset && newHeight === height) return;
  indent = newIndent;
  offset = newOffset;
  height = newHeight;
  win.setFrameDisplayAnimate(frameFor(indent, offset, height), true, true);
  view.setFrame($.NSMakeRect(0, 0, cfg.width, height));
  layoutText();
}

// Tell Go how much height the screen offers, so it can start a new column when a stack would run off the bottom.
emit('screen ' + Math.round(area.size.height - 2 * margin));

let expanded = false;
function press(spec) {
  if (spec.type === 'dismiss') finish('dismissed');
  else if (spec.type === 'goto') finish('opened');
  else if (spec.type === 'preview') {
    expanded = !expanded;
    // Someone reading the full text should not have it vanish: stop the auto-dismiss timer.
    if (expanded && timer) { timer.invalidate; timer = null; }
    const button = buttons[cfg.buttons.indexOf(spec)];
    button.setTitle($(expanded ? (spec.collapseLabel || 'Collapse') : spec.label));
    // Resize at once, then tell Go so it can move the banners after this one; Go answers with the same height and so changes nothing.
    layoutTo(indent, offset, expanded ? Math.max(cfg.previewHeight, cfg.height) : cfg.height);
    emit('height ' + height);
  }
}

// Go sends {"offset": n, "height": h} lines to move this banner when others come, go, grow or shrink.
if (cfg.managed) {
  const stdin = $.NSFileHandle.fileHandleWithStandardInput;
  let pending = '';
  ObjC.registerSubclass({
    name: 'ShhBannerInput',
    superclass: 'NSObject',
    methods: {
      'ready:': {
        types: ['void', ['id']],
        implementation: function () {
          const data = stdin.availableData;
          if (data.length === 0) return; // Go is gone: carry on alone, silently
          pending += $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding).js;
          let end;
          while ((end = pending.indexOf('\n')) >= 0) {
            const line = pending.slice(0, end);
            pending = pending.slice(end + 1);
            try { const m = JSON.parse(line); layoutTo(m.indent || 0, m.offset, m.height); } catch (e) { /* ignore a malformed line */ }
          }
          stdin.waitForDataInBackgroundAndNotify;
        },
      },
    },
  });
  const input = $.ShhBannerInput.alloc.init;
  $.NSNotificationCenter.defaultCenter.addObserverSelectorNameObject(input, 'ready:', $.NSFileHandleDataAvailableNotification, stdin);
  stdin.waitForDataInBackgroundAndNotify;
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
if (!cfg.managed || attached()) console.log(outcome);
