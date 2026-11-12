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
// The text is drawn in its own color (white when none is given).
const textHex = (cfg.textColor || '#ffffff').replace('#', '');
const textChannel = i => parseInt(textHex.slice(i, i + 2), 16) / 255;
const textColor = $.NSColor.colorWithSRGBRedGreenBlueAlpha(textChannel(0), textChannel(2), textChannel(4), 1);

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
  field.setTextColor(textColor);
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

// Layout, in points, matching the Go side: 12 of padding, an optional 14 point header line naming the app, a 20 point title line, a
// 4 point gap, 16 points per body line, and a 32 point row at the bottom for each row of buttons (the dismiss button is not one of
// them: it sits in the top right corner). The caller sized the banner to its text and its buttons; text beyond what fits ends in an
// ellipsis.
const headerRow = cfg.header ? 14 : 0;
const buttonRow = 32 * (cfg.buttonRows !== undefined ? cfg.buttonRows : (cfg.buttons.some(b => b.type !== 'dismiss') ? 1 : 0));
// How much of the top right corner the dismiss button takes from the title and the app name, which are cut short to leave it room.
let cornerWidth = 0;
const textWidth = () => cfg.width - 32 - (cornerWidth ? cornerWidth + 8 : 0);
const header = cfg.header ? label(cfg.header, 11, true, $.NSMakeRect(16, height - 12 - headerRow, textWidth(), headerRow), 1) : null;
if (header) header.setTextColor(textColor.colorWithAlphaComponent(0.75));
const title = label(cfg.title, 15, true, $.NSMakeRect(16, height - 12 - headerRow - 20, textWidth(), 20), 1);
const body = label(cfg.body, 13, false, $.NSMakeRect(16, 12 + buttonRow, cfg.width - 32, height - 12 - headerRow - 20 - 4 - 12 - buttonRow), 1);
function layoutText() {
  const room = height - 12 - headerRow - 20 - 4 - 12 - buttonRow;
  if (header) header.setFrame($.NSMakeRect(16, height - 12 - headerRow, textWidth(), headerRow));
  title.setFrame($.NSMakeRect(16, height - 12 - headerRow - 20, textWidth(), 20));
  body.setFrame($.NSMakeRect(16, 12 + buttonRow, cfg.width - 32, room));
  body.setMaximumNumberOfLines(Math.max(1, Math.floor(room / 16)));
}
layoutText();

// Buttons look like the system's unless the configuration gives them a color: then they are drawn as a patch of that color with the
// button text color on it (the banner's own text color when only a button color was given, and a faint white patch when only a text
// color was). A bezel cannot be recolored, so a colored button has none: its face is a box behind it and its text a label of its own on
// that box, with the (now empty and invisible) button on top to take the clicks. Both are things the banner is already made of, which
// is the point: the two ways of coloring a button's own title do not work from here (JXA cannot call NSAttributedString's
// initializers, and a content tint did nothing in the banner's window). Anything that goes wrong leaves the system look.
const buttonHex = h => {
  const rgb = h.replace('#', '');
  const ch = i => parseInt(rgb.slice(i, i + 2), 16) / 255;
  return $.NSColor.colorWithSRGBRedGreenBlueAlpha(ch(0), ch(2), ch(4), 1);
};
const buttonFill = cfg.buttonColor ? buttonHex(cfg.buttonColor)
  : (cfg.buttonTextColor ? $.NSColor.colorWithSRGBRedGreenBlueAlpha(1, 1, 1, 0.25) : null);
const buttonInk = cfg.buttonTextColor ? buttonHex(cfg.buttonTextColor) : (buttonFill ? textColor : null);
// A painted button says its words through its label, and the button itself says nothing.
const inks = [];
const measure = text => $(text).sizeWithAttributes($.NSDictionary.dictionaryWithObjectForKey($.NSFont.systemFontOfSize(12), $.NSFontAttributeName)).width;
const saying = []; // what each button says now
function setButtonTitle(button, text, i) {
  saying[i] = text;
  if (inks[i]) {
    inks[i].setStringValue($(text));
    button.setTitle($(''));
  } else {
    button.setTitle($(text));
  }
  layoutButtons(); // the new words may be a different width, so the buttons are placed again
}
// A button with a color has no bezel of its own: a box of that color sits behind it as its face (the same kind of box the banner itself
// is made of), its words are a label over the box, and layoutButtons keeps both under the button.
const backdrops = [];
function paintButton(button, i, text) {
  if (!buttonFill) return;
  try {
    button.setBordered(false);
    const box = $.NSBox.alloc.initWithFrame($.NSMakeRect(0, 0, 10, 10));
    box.setBoxType($.NSBoxCustom);
    box.setTitlePosition($.NSNoTitle);
    box.setBorderWidth(0);
    box.setCornerRadius(6);
    box.setFillColor(buttonFill);
    view.addSubview(box); // before the button is added, so it is behind it
    const ink = $.NSTextField.labelWithString($(text));
    ink.setFont($.NSFont.systemFontOfSize(12));
    ink.setTextColor(buttonInk);
    view.addSubview(ink);
    backdrops[i] = box;
    inks[i] = ink;
    button.setTitle($(''));
  } catch (e) { button.setBordered(true); button.setTitle($(text)); /* the system's look stays */ }
}

const actions = $.ShhBannerActions.alloc.init;
const buttons = cfg.buttons.map((spec, i) => {
  const button = $.ShhBannerButton.alloc.init;
  button.setBezelStyle($.NSBezelStyleRounded);
  button.setControlSize($.NSControlSizeSmall);
  button.setFont($.NSFont.systemFontOfSize(12));
  button.setTag(i);
  button.setTarget(actions);
  button.setAction('press:');
  button.setTitle($(spec.label));
  saying[i] = spec.label;
  paintButton(button, i, spec.label);
  view.addSubview(button);
  return button;
});
// How wide a button is, from the words on it: their width in the button's font and ten points of padding either side, which is what the
// system makes a small button. The Go side works the same out from a table of the widths of the letters, so the rows it reserved are
// the rows used here.
function naturalWidth(i) {
  return Math.ceil(measure(saying[i])) + 20;
}

// The dismiss button is in the top right corner, as wide as its words. The other visible buttons are laid out left to right in the
// configured order, each as wide as its words, and go on to a new row when the next one would not fit. The rows were reserved by the
// Go side from a generous guess at the widths, and these are the real ones, so there are never more rows than room.
function layoutButtons() {
  const gap = 8;
  const avail = cfg.width - 32;
  const rows = Math.max(1, cfg.buttonRows || 1);
  let x = 0;
  let row = 0;
  let corner = 0;
  buttons.forEach((b, i) => {
    if (b.isHidden) return;
    const width = Math.min(naturalWidth(i), avail);
    if (cfg.buttons[i].type === 'dismiss') {
      b.setFrame($.NSMakeRect(cfg.width - 16 - width, height - 10 - 22, width, 22));
      corner = width;
      return;
    }
    if (x > 0 && x + width > avail) { row++; x = 0; }
    // the first row is the top of the area under the text, so the rows that were not needed are the ones left empty at the bottom
    b.setFrame($.NSMakeRect(16 + x, 8 + Math.max(0, rows - 1 - row) * 32, width, 24));
    x += width + gap;
  });
  if (corner !== cornerWidth) {
    cornerWidth = corner;
    layoutText();
  }
  buttons.forEach((b, i) => {
    const box = backdrops[i];
    if (!box) return;
    box.setHidden(b.isHidden);
    inks[i].setHidden(b.isHidden);
    if (!b.isHidden) {
      box.setFrame(b.frame);
      // The label is centered on the button by hand, from the size its words take. (Its alignment setting is not used: the constant
      // that is named center came out as right-aligned text on the macOS this was tried on.)
      inks[i].sizeToFit;
      const size = inks[i].frame.size;
      const face = b.frame;
      inks[i].setFrame($.NSMakeRect(face.origin.x + (face.size.width - size.width) / 2, face.origin.y + (face.size.height - size.height) / 2, size.width, size.height));
    }
  });
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
  layoutButtons(); // the dismiss button is placed from the top, so it has to follow the new height
}

// Tell Go how much height the screen offers, so it can start a new column when a stack would run off the bottom.
emit('screen ' + Math.round(area.size.height - 2 * margin));

let expanded = false;
let flagged = false;
let trainedOnce = false;
function press(spec) {
  if (spec.type === 'dismiss') finish('dismissed');
  else if (spec.type === 'goto') finish('opened');
  else if (spec.type === 'action') {
    // A button an alert brought with it: tell Go which one, then go away like Dismiss. The line is written before the script stops.
    emit('action ' + spec.id);
    finish('dismissed');
  }
  else if (spec.type === 'train') {
    // One press: the notification is marked as one that should not have alerted, the button says so and stops being pressable.
    const i = cfg.buttons.indexOf(spec);
    if (!trainedOnce) {
      trainedOnce = true;
      setButtonTitle(buttons[i], spec.trainedLabel || 'Noted', i);
      buttons[i].setEnabled(false);
      emit('train 1');
    }
  } else if (spec.type === 'flag') {
    // A toggle: pressing it marks the notification for review in the Train tab and shows the flagged label; pressing it again clears
    // the mark. The script tells Go the state it now has (1 or 0), not just that it was pressed, so the two cannot drift apart.
    const button = buttons[cfg.buttons.indexOf(spec)];
    flagged = !flagged;
    setButtonTitle(button, flagged ? (spec.flaggedLabel || 'Flagged') : spec.label, cfg.buttons.indexOf(spec));
    emit('flag ' + (flagged ? 1 : 0));
  } else if (spec.type === 'preview') {
    expanded = !expanded;
    // Someone reading the full text should not have it vanish: stop the auto-dismiss timer.
    if (expanded && timer) { timer.invalidate; timer = null; }
    const button = buttons[cfg.buttons.indexOf(spec)];
    setButtonTitle(button, expanded ? (spec.collapseLabel || 'Collapse') : spec.label, cfg.buttons.indexOf(spec));
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
